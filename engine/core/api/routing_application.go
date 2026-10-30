package api

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"NekkoVPN/core/engine"
	"NekkoVPN/core/presets"
	"NekkoVPN/core/routing"
)

const routingDebounce = 1500 * time.Millisecond

type RoutingApplicationSnapshot struct {
	Sequence        uint64               `json:"sequence"`
	Revision        uint64               `json:"revision"`
	AppliedRevision uint64               `json:"appliedRevision"`
	Status          string               `json:"status"`
	Error           string               `json:"error"`
	AppliedAt       int64                `json:"appliedAt"`
	Preview         bool                 `json:"preview"`
	Manual          routing.RoutingState `json:"manual"`
	Effective       routing.RoutingState `json:"effective"`
}

type routingBatch struct{ manual, effective routing.RoutingState }

type routingApplication struct {
	// Core.operationMu owns commands/timer/transactions. A separate tiny lock
	// lets the UI read applying/failed while a slow backend call is in flight.
	mu       sync.RWMutex
	snapshot RoutingApplicationSnapshot
	pending  *routingBatch
	timer    *time.Timer
	revision uint64
}

func newRoutingApplication(manual routing.RoutingState, preview bool) *routingApplication {
	return &routingApplication{snapshot: RoutingApplicationSnapshot{Status: "idle", Preview: preview, Manual: manual.Clone(), Effective: manual.Clone()}}
}

func (c *Core) RoutingStatus() RoutingApplicationSnapshot {
	c.mu.Lock()
	app := c.routingApp
	c.mu.Unlock()
	if app == nil {
		return RoutingApplicationSnapshot{Status: "idle"}
	}
	app.mu.RLock()
	defer app.mu.RUnlock()
	result := app.snapshot
	result.Manual, result.Effective = result.Manual.Clone(), result.Effective.Clone()
	return result
}

// Caller owns operationMu, or is the single-threaded initialization path.
func (c *Core) manualRouting() (routing.RoutingState, error) {
	if c.routingApp == nil {
		return c.routing.ReadRouting()
	}
	c.routingApp.mu.RLock()
	defer c.routingApp.mu.RUnlock()
	return c.routingApp.snapshot.Manual.Clone(), nil
}

func (c *Core) publishRouting(status string, err error, manual, effective routing.RoutingState, applied bool) {
	app := c.routingApp
	app.mu.Lock()
	defer app.mu.Unlock()
	next := app.snapshot
	next.Sequence++
	next.Revision, next.Status, next.Error = app.revision, status, ""
	if err != nil {
		next.Error = err.Error()
	}
	next.Manual, next.Effective = manual.Clone(), effective.Clone()
	if applied {
		next.AppliedRevision, next.AppliedAt = app.revision, time.Now().UnixMilli()
	}
	app.snapshot = next
}

func (c *Core) cancelRoutingTimer() {
	if c.routingApp != nil && c.routingApp.timer != nil {
		c.routingApp.timer.Stop()
		c.routingApp.timer = nil
	}
}

// transactionRouting is the single backend/configuration boundary for all
// routing sources. persist runs only after the adapter accepted the policy.
// A failed recovery is returned along with the original error, never hidden.
func (c *Core) transactionRouting(previous, next routing.RoutingState, persist, restore func() error) error {
	if c.routingPreview || reflect.DeepEqual(previous, next) {
		if err := persist(); err != nil {
			return errors.Join(err, restore())
		}
		return nil
	}
	wasRunning := c.backend.IsRunning()
	restart := wasRunning && !routing.ActiveEqual(previous, next)
	startAttempted := false
	rollback := func(cause error) error {
		var recovery []error
		// A failed Start may leave a partially running process. Stop that candidate
		// before reinstalling the previous configuration.
		stopped := true
		if startAttempted && c.backend.IsRunning() {
			if err := c.backend.Stop(); err != nil {
				recovery = append(recovery, fmt.Errorf("остановка неудачной конфигурации: %w", err))
				stopped = false
			}
		}
		if err := restore(); err != nil {
			recovery = append(recovery, fmt.Errorf("восстановление правил: %w", err))
		}
		configured := true
		if err := c.configureRouting(previous); err != nil {
			recovery = append(recovery, fmt.Errorf("восстановление конфигурации: %w", err))
			configured = false
		}
		if wasRunning && restart && configured && stopped && !c.backend.IsRunning() {
			if err := c.backend.Start(c.ctx); err != nil {
				recovery = append(recovery, fmt.Errorf("восстановление VPN: %w", err))
			}
		}
		return errors.Join(append([]error{cause}, recovery...)...)
	}
	if err := c.configureRouting(next); err != nil {
		return rollback(err)
	}
	if restart {
		if err := c.backend.Stop(); err != nil {
			return rollback(err)
		}
		startAttempted = true
		if err := c.backend.Start(c.ctx); err != nil {
			return rollback(err)
		}
	}
	if err := persist(); err != nil {
		// Successful Start also has to be undone after a persistence failure.
		return rollback(err)
	}
	return nil
}

func (c *Core) changeManualRouting(mutate func(*routing.RoutingState) error) error {
	old, err := c.manualRouting()
	if err != nil {
		return err
	}
	next := old.Clone()
	if err := mutate(&next); err != nil {
		return err
	}
	effective, err := c.effectiveRouting(next)
	if err != nil {
		return err
	}
	if err := engine.ValidateConfiguration(engine.Configuration{Routing: effective}, c.backend.Info().Capabilities); err != nil {
		return err
	}
	if reflect.DeepEqual(old, next) {
		return nil
	}
	app := c.routingApp
	if app.pending == nil {
		previous, err := c.effectiveRouting(old)
		if err != nil {
			return err
		}
		app.pending = &routingBatch{manual: old, effective: previous}
	}
	app.revision++
	c.publishRouting("pending", nil, next, effective, false)
	c.cancelRoutingTimer()
	if c.routingPreview || !c.backend.IsRunning() {
		return c.flushRoutingLocked()
	}
	revision, ctx := app.revision, c.ctx
	app.timer = time.AfterFunc(routingDebounce, func() {
		c.operationMu.Lock()
		defer c.operationMu.Unlock()
		if ctx.Err() != nil || app.revision != revision || app.pending == nil {
			return
		}
		if err := c.flushRoutingLocked(); err != nil {
			c.logger.Logf("routing apply failed: %v", err)
		}
	})
	return nil
}

func (c *Core) FlushRouting() error {
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	return c.flushRoutingLocked()
}

func (c *Core) flushRoutingLocked() error {
	app := c.routingApp
	if app == nil || app.pending == nil {
		return nil
	}
	c.cancelRoutingTimer()
	batch := app.pending
	manual, err := c.manualRouting()
	if err != nil {
		return err
	}
	next, err := c.effectiveRouting(manual)
	if err != nil {
		return err
	}
	c.publishRouting("applying", nil, manual, next, false)
	err = c.transactionRouting(batch.effective, next,
		func() error { return c.routing.WriteRouting(manual) },
		func() error { return c.routing.WriteRouting(batch.manual) })
	app.pending = nil
	if err != nil {
		c.publishRouting("failed", err, batch.manual, batch.effective, false)
		return err
	}
	status := "applied"
	if c.routingPreview {
		status = "preview"
	}
	c.publishRouting(status, nil, manual, next, !c.routingPreview)
	return nil
}

func (c *Core) commitPresetState(ctx context.Context, old, next presets.State, persist func() error) error {
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	if ctx.Err() != nil {
		return fmt.Errorf("движок остановлен")
	}
	app := c.routingApp
	manual, err := c.manualRouting()
	if err != nil {
		return err
	}
	previous, err := presets.Effective(manual, old.Catalog, old.Choices)
	if err != nil {
		return err
	}
	beforeManual := manual
	if app.pending != nil {
		previous, beforeManual = app.pending.effective, app.pending.manual
	}
	effective, err := presets.Effective(manual, next.Catalog, next.Choices)
	if err != nil {
		return err
	}
	if err := engine.ValidateConfiguration(engine.Configuration{Routing: effective}, c.backend.Info().Capabilities); err != nil {
		return err
	}
	c.cancelRoutingTimer()
	app.revision++
	c.publishRouting("applying", nil, manual, effective, false)
	err = c.transactionRouting(previous, effective, func() error {
		if err := c.routing.WriteRouting(manual); err != nil {
			return err
		}
		return persist()
	}, func() error { return c.routing.WriteRouting(beforeManual) })
	app.pending = nil
	if err != nil {
		c.publishRouting("failed", err, beforeManual, previous, false)
		return err
	}
	c.presetState = next
	status := "applied"
	if c.routingPreview {
		status = "preview"
	}
	c.publishRouting(status, nil, manual, effective, !c.routingPreview)
	return nil
}

// Compatibility hook for typed callers/tests using the former callback.
func (c *Core) applyPresetState(ctx context.Context, state presets.State) error {
	c.operationMu.Lock()
	old := c.presetState
	c.operationMu.Unlock()
	return c.commitPresetState(ctx, old, state, func() error { return nil })
}
