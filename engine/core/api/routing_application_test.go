package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"NekkoVPN/core/engine"
	"NekkoVPN/core/presets"
	"NekkoVPN/core/routing"
)

type routingEngine struct {
	alternateEngine
	stopCalls                       int
	startFailures, stopFailures     int
	configureGate, configureEntered chan struct{}
}

func (f *routingEngine) Start(ctx context.Context) error {
	f.mu.Lock()
	if f.startFailures > 0 {
		f.startFailures--
		f.mu.Unlock()
		return errors.New("injected Start failure")
	}
	f.mu.Unlock()
	return f.alternateEngine.Start(ctx)
}
func (f *routingEngine) Stop() error {
	f.mu.Lock()
	f.stopCalls++
	if f.stopFailures > 0 {
		f.stopFailures--
		f.mu.Unlock()
		return errors.New("injected Stop failure")
	}
	f.mu.Unlock()
	return f.alternateEngine.Stop()
}
func (f *routingEngine) Configure(configuration engine.Configuration) error {
	f.mu.Lock()
	gate, entered := f.configureGate, f.configureEntered
	f.mu.Unlock()
	if gate != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-gate
	}
	return f.alternateEngine.Configure(configuration)
}
func routingCore(t *testing.T, running bool) (*Core, *routingEngine) {
	t.Helper()
	f := &routingEngine{alternateEngine: alternateEngine{caps: engine.Capabilities{TUN: true, DomainRouting: true, IPRouting: true, ProcessRouting: true}}}
	c := New()
	if err := c.InitWithEngine(manualRoutingDir(t), func(string, engine.Logger) (engine.Backend, error) { return f, nil }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Shutdown() })
	if running {
		if _, err := c.AddProfileFromURI("Server"); err != nil {
			t.Fatal(err)
		}
		if err := c.StartVPN(); err != nil {
			t.Fatal(err)
		}
	}
	return c, f
}

func TestRoutingBatchHasOneRestartAndNoUncommittedDiskWrites(t *testing.T) {
	c, f := routingCore(t, true)
	for _, domain := range []string{"one.example", "two.example", "three.example"} {
		if err := c.AddRule("bypass", "domains", domain); err != nil {
			t.Fatal(err)
		}
	}
	status := c.RoutingStatus()
	if status.Status != "pending" || status.Revision != 3 || status.AppliedRevision != 0 {
		t.Fatal(status)
	}
	persisted, err := c.routing.ReadRouting()
	if err != nil || len(persisted.Bypass.Domains) != 0 {
		t.Fatal("uncommitted rules reached disk", persisted, err)
	}
	deadline := time.Now().Add(4 * time.Second)
	for (c.RoutingStatus().Status == "pending" || c.RoutingStatus().Status == "applying") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	status = c.RoutingStatus()
	if status.Status != "applied" || status.AppliedRevision != 3 {
		t.Fatal(status)
	}
	f.mu.Lock()
	starts := f.starts
	domains := append([]string{}, f.configuration.Routing.Bypass.Domains...)
	f.mu.Unlock()
	if starts != 2 || len(domains) != 3 {
		t.Fatalf("starts=%d domains=%v", starts, domains)
	}
	persisted, _ = c.routing.ReadRouting()
	if len(persisted.Bypass.Domains) != 3 {
		t.Fatal("committed state not saved")
	}
}

func TestPresetAndPendingManualRulesShareOneTransaction(t *testing.T) {
	c, f := routingCore(t, true)
	if err := c.AddRule("bypass", "domains", "manual.example"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetServicePreset("bypass", "steam", true); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	starts := f.starts
	domains := append([]string{}, f.configuration.Routing.Bypass.Domains...)
	f.mu.Unlock()
	if starts != 2 || !strings.Contains(strings.Join(domains, ","), "manual.example") || len(domains) < 2 {
		t.Fatalf("starts=%d rules=%v", starts, domains)
	}
	if c.RoutingStatus().Status != "applied" || c.routingApp.pending != nil {
		t.Fatal(c.RoutingStatus())
	}
}

func TestInactiveModeAndDuplicateEditsDoNotRestart(t *testing.T) {
	c, f := routingCore(t, true)
	if err := c.AddRule("proxy", "domains", "inactive.example"); err != nil {
		t.Fatal(err)
	}
	if err := c.FlushRouting(); err != nil {
		t.Fatal(err)
	}
	if err := c.AddRule("proxy", "domains", "inactive.example"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	starts := f.starts
	f.mu.Unlock()
	if starts != 1 || c.RoutingStatus().Revision != 1 {
		t.Fatalf("starts=%d status=%+v", starts, c.RoutingStatus())
	}
}

func TestRoutingFailuresRestoreManualRuntimeAndReportFailed(t *testing.T) {
	for _, failure := range []string{"start", "stop", "configure", "persist", "recovery"} {
		t.Run(failure, func(t *testing.T) {
			c, f := routingCore(t, true)
			if err := c.AddRule("bypass", "domains", "candidate.example"); err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			switch failure {
			case "start":
				f.startFailures = 1
			case "stop":
				f.stopFailures = 1
			case "configure":
				f.configureErr = errors.New("injected Configure failure")
			case "recovery":
				f.startFailures = 2
			}
			f.mu.Unlock()
			if failure == "persist" {
				c.routing = routing.New(t.TempDir())
			}
			err := c.FlushRouting()
			if err == nil {
				t.Fatal("failure swallowed")
			}
			status := c.RoutingStatus()
			if status.Status != "failed" || status.Error == "" || status.AppliedRevision != 0 || len(status.Manual.Bypass.Domains) != 0 {
				t.Fatal(status)
			}
			if failure == "recovery" && !strings.Contains(err.Error(), "восстановление VPN") {
				t.Fatal("recovery failure hidden", err)
			}
			if failure == "persist" && !strings.Contains(err.Error(), "восстановление правил") {
				t.Fatal("disk recovery failure hidden", err)
			}
			f.mu.Lock()
			running := f.running
			domains := f.configuration.Routing.Bypass.Domains
			f.configureErr = nil
			f.mu.Unlock()
			if failure != "recovery" && (!running || len(domains) != 0) {
				t.Fatalf("old runtime not restored: running=%v rules=%v", running, domains)
			}
		})
	}
}

func TestRoutingStatusReadableDuringSlowApply(t *testing.T) {
	c, f := routingCore(t, true)
	if err := c.AddRule("bypass", "domains", "slow.example"); err != nil {
		t.Fatal(err)
	}
	gate, entered := make(chan struct{}), make(chan struct{}, 1)
	f.mu.Lock()
	f.configureGate, f.configureEntered = gate, entered
	f.mu.Unlock()
	done := make(chan error, 1)
	go func() { done <- c.FlushRouting() }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("apply did not enter backend")
	}
	snapshot := make(chan RoutingApplicationSnapshot, 1)
	go func() { snapshot <- c.RoutingStatus() }()
	select {
	case status := <-snapshot:
		if status.Status != "applying" {
			t.Error(status)
		}
	case <-time.After(time.Second):
		t.Error("status blocked behind backend")
	}
	close(gate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestShutdownCommitsPendingWithoutRestartOrLateTimer(t *testing.T) {
	c, f := routingCore(t, true)
	if err := c.AddRule("bypass", "domains", "saved.example"); err != nil {
		t.Fatal(err)
	}
	if err := c.Shutdown(); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	starts, running := f.starts, f.running
	f.mu.Unlock()
	if starts != 1 || running || c.routingApp.timer != nil {
		t.Fatalf("shutdown restarted engine: %d %v", starts, running)
	}
	state, err := c.routing.ReadRouting()
	if err != nil || !reflect.DeepEqual(state.Bypass.Domains, []string{"saved.example"}) {
		t.Fatal(state, err)
	}
}

func TestPreviewUsesGoCommandsWithoutDiskOrBackendWrites(t *testing.T) {
	f := &routingEngine{alternateEngine: alternateEngine{caps: engine.Capabilities{DomainRouting: true, IPRouting: true, ProcessRouting: true}}}
	c := New()
	c.routingPreview = true
	dir := t.TempDir()
	if err := c.InitWithEngine(dir, func(string, engine.Logger) (engine.Backend, error) { return f, nil }); err != nil {
		t.Fatal(err)
	}
	defer c.Shutdown()
	if err := c.AddRule("bypass", "domains", "https://Example.com/path"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetServicePreset("bypass", "steam", false); err != nil {
		t.Fatal(err)
	}
	status := c.RoutingStatus()
	if status.Status != "preview" || !status.Preview || status.AppliedRevision != 0 || status.Manual.Bypass.Domains[0] != "example.com" {
		t.Fatal(status)
	}
	if _, err := os.Stat(filepath.Join(dir, "routing.json")); !os.IsNotExist(err) {
		t.Fatal("preview wrote manual file", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "routing-presets.json")); !os.IsNotExist(err) {
		t.Fatal("preview wrote catalog file", err)
	}
	if err := c.StopVPN(); err != nil {
		t.Fatal(err)
	}
	if err := c.StartVPN(); err == nil {
		t.Fatal("preview started VPN")
	}
	if err := c.RestartVPN(); err == nil {
		t.Fatal("preview restarted VPN")
	}
	if err := c.Shutdown(); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.starts != 0 || f.stopCalls != 0 || len(f.configuration.Routing.Bypass.Domains) != 0 {
		t.Fatal("preview touched native backend")
	}
}

func TestCatalogPersistenceFailureRestoresPendingBatch(t *testing.T) {
	c, f := routingCore(t, true)
	path := filepath.Join(t.TempDir(), "catalog.json")
	m, err := presets.New(path, presets.Options{Transact: func(old, next presets.State, persist func() error) error {
		return c.commitPresetState(c.ctx, old, next, persist)
	}})
	if err != nil {
		t.Fatal(err)
	}
	c.presets = m
	for _, mode := range []routing.RoutingMode{routing.ModeBypass, routing.ModeProxy} {
		for _, service := range m.Snapshot().Catalog.Services {
			if err := m.Select(mode, service.ID, false); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := c.AddRule("bypass", "domains", "not-committed.example"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetServicePreset("bypass", "steam", true); err == nil {
		t.Fatal("catalog persistence error swallowed")
	}
	status := c.RoutingStatus()
	if status.Status != "failed" || len(status.Manual.Bypass.Domains) != 0 {
		t.Fatal(status)
	}
	if presets.Selected(m.Snapshot().Catalog, m.Snapshot().Choices, routing.ModeBypass, "steam") {
		t.Fatal("failed group published")
	}
	state, err := c.routing.ReadRouting()
	if err != nil || len(state.Bypass.Domains) != 0 {
		t.Fatal("manual batch persisted on catalog failure", state, err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.running || len(f.configuration.Routing.Bypass.Domains) != 0 {
		t.Fatal("old runtime not recovered")
	}
}

func TestExplicitRestartDoesNotSkipForInactivePendingEdits(t *testing.T) {
	for _, mode := range []string{"bypass", "proxy"} {
		t.Run(mode, func(t *testing.T) {
			c, f := routingCore(t, true)
			if err := c.AddRule(mode, "domains", "restart.example"); err != nil {
				t.Fatal(err)
			}
			if err := c.RestartVPN(); err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			starts := f.starts
			f.mu.Unlock()
			if starts != 2 {
				t.Fatalf("explicit restart count=%d, expected exactly one restart", starts)
			}
		})
	}
}
