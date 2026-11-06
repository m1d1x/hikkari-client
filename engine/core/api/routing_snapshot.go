package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"NekkoVPN/core/engine"
	"NekkoVPN/core/presets"
	"NekkoVPN/core/routing"
)

type RoutingSnapshot = presets.RoutingSnapshot

// ExportRoutingSnapshot serializes current group choices and manual routing rules into readable JSON.
func (c *Core) ExportRoutingSnapshot() (string, error) {
	c.operationMu.Lock()
	defer c.operationMu.Unlock()

	manual, err := c.manualRouting()
	if err != nil {
		return "", err
	}

	var cat presets.Catalog
	var ch presets.Choices
	if c.presets != nil {
		s := c.presets.Snapshot().State
		cat, ch = s.Catalog, s.Choices
	} else {
		cat, _ = presets.BundledCatalog()
		ch = presets.Choices{}
	}

	snap := presets.BuildRoutingSnapshot(manual, cat, ch)
	raw, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return "", fmt.Errorf("ошибка сериализации снимка: %w", err)
	}
	return string(raw), nil
}

// ImportRoutingSnapshot validates and atomically applies the given JSON snapshot.
func (c *Core) ImportRoutingSnapshot(data string) error {
	if strings.TrimSpace(data) == "" {
		return fmt.Errorf("пустые данные для импорта")
	}

	snap, err := presets.ParseRoutingSnapshot([]byte(data))
	if err != nil {
		return err
	}

	c.operationMu.Lock()
	defer c.operationMu.Unlock()

	if c.ctx != nil && c.ctx.Err() != nil {
		return fmt.Errorf("движок остановлен")
	}

	manual, err := c.manualRouting()
	if err != nil {
		return err
	}

	var oldPresetState presets.State
	if c.presets != nil {
		oldPresetState = c.presets.Snapshot().State
	} else {
		cat, _ := presets.BundledCatalog()
		oldPresetState = presets.State{Catalog: cat, Choices: presets.Choices{}}
	}

	nextRouting, nextChoices, err := presets.ApplyRoutingSnapshotToState(snap, manual, oldPresetState.Catalog)
	if err != nil {
		return err
	}

	nextPresetState := presets.Clone(oldPresetState)
	nextPresetState.Choices = nextChoices

	effective, err := presets.Effective(nextRouting, nextPresetState.Catalog, nextPresetState.Choices)
	if err != nil {
		return err
	}

	if err := engine.ValidateConfiguration(engine.Configuration{Routing: effective}, c.backend.Info().Capabilities); err != nil {
		return fmt.Errorf("конфигурация не поддерживается: %w", err)
	}

	return c.applyRoutingAndPresetsTransaction(manual, nextRouting, oldPresetState, nextPresetState, effective)
}

// ResetToDefaults resets all custom user overrides and restores pristine baseline from defaults.json.
func (c *Core) ResetToDefaults() error {
	c.operationMu.Lock()
	defer c.operationMu.Unlock()

	if c.ctx != nil && c.ctx.Err() != nil {
		return fmt.Errorf("движок остановлен")
	}

	manual, err := c.manualRouting()
	if err != nil {
		return err
	}

	bundledCat, err := presets.BundledCatalog()
	if err != nil {
		return err
	}

	var oldPresetState presets.State
	if c.presets != nil {
		oldPresetState = c.presets.Snapshot().State
	} else {
		oldPresetState = presets.State{Catalog: bundledCat, Choices: presets.Choices{}}
	}

	nextRouting := routing.DefaultRoutingState()
	nextPresetState := presets.Clone(oldPresetState)
	nextPresetState.Catalog = bundledCat
	nextPresetState.Choices = presets.Choices{}
	nextPresetState.Source = "bundled"

	effective, err := presets.Effective(nextRouting, bundledCat, nextPresetState.Choices)
	if err != nil {
		return err
	}

	if err := engine.ValidateConfiguration(engine.Configuration{Routing: effective}, c.backend.Info().Capabilities); err != nil {
		return fmt.Errorf("конфигурация не поддерживается: %w", err)
	}

	return c.applyRoutingAndPresetsTransaction(manual, nextRouting, oldPresetState, nextPresetState, effective)
}

func (c *Core) applyRoutingAndPresetsTransaction(
	oldManual, nextManual routing.RoutingState,
	oldPresets, nextPresets presets.State,
	nextEffective routing.RoutingState,
) error {
	app := c.routingApp
	previous, err := presets.Effective(oldManual, oldPresets.Catalog, oldPresets.Choices)
	if err != nil {
		return err
	}
	beforeManual := oldManual
	if app != nil && app.pending != nil {
		previous, beforeManual = app.pending.effective, app.pending.manual
	}

	c.cancelRoutingTimer()
	if app != nil {
		app.revision++
		c.publishRouting("applying", nil, nextManual, nextEffective, false)
	}

	persist := func() error {
		if err := c.routing.WriteRouting(nextManual); err != nil {
			return err
		}
		if c.presets != nil {
			return c.presets.PersistState(nextPresets)
		}
		return nil
	}

	restore := func() error {
		var errs []error
		if err := c.routing.WriteRouting(beforeManual); err != nil {
			errs = append(errs, err)
		}
		if c.presets != nil {
			if err := c.presets.PersistState(oldPresets); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	}

	err = c.transactionRouting("routing-snapshot", previous, nextEffective, persist, restore)
	if app != nil {
		app.pending = nil
	}

	if err != nil {
		if app != nil {
			c.publishRouting("failed", err, beforeManual, previous, false)
		}
		return err
	}

	if c.presets != nil {
		c.presets.SetStateDirect(nextPresets)
	}
	c.presetState = nextPresets

	status := "applied"
	if c.routingPreview {
		status = "preview"
	}
	if app != nil {
		c.publishRouting(status, nil, nextManual, nextEffective, !c.routingPreview)
	}
	return nil
}
