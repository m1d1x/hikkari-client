package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"

	"NekkoVPN/core/engine"
	"NekkoVPN/core/presets"
)

func TestServicePresetUpdatesReachRunningBackendAndPreserveManualRules(t *testing.T) {
	var version atomic.Int64
	version.Store(3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"schemaVersion":1,"version":%d,"services":[{"id":"test","name":"Test","icon":"steam","domains":["service%d.example"],"ips":[],"processes":{},"defaultModes":[]}]}`, version.Load(), version.Load())
	}))
	defer server.Close()
	t.Setenv("HIKKARI_PRESET_CATALOG_URL", server.URL)
	t.Setenv("HIKKARI_PRESET_EVENTS_URL", "")
	f := &alternateEngine{caps: engine.Capabilities{TUN: true, DomainRouting: true, IPRouting: true, ProcessRouting: true}}
	c := initAlternate(t, f)
	if err := c.RefreshRoutingPresets(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddProfileFromURI("first"); err != nil {
		t.Fatal(err)
	}
	if err := c.AddRule("bypass", "domains", "manual.example"); err != nil {
		t.Fatal(err)
	}
	if err := c.StartVPN(); err != nil {
		t.Fatal(err)
	}
	if err := c.SetServicePreset("bypass", "test", true); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	starts := f.starts
	got := f.configuration.Routing.Bypass.Domains
	f.mu.Unlock()
	if !reflect.DeepEqual(got, []string{"manual.example", "service3.example"}) {
		t.Fatal("group not applied", got)
	}
	version.Store(4)
	if err := c.RefreshRoutingPresets(); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	got = f.configuration.Routing.Bypass.Domains
	after := f.starts
	f.mu.Unlock()
	if !reflect.DeepEqual(got, []string{"manual.example", "service4.example"}) || after != starts+1 {
		t.Fatal("live update did not replace group and restart backend", got, after, starts)
	}
	manual, _ := c.Routing()
	if !reflect.DeepEqual(manual.Bypass.Domains, []string{"manual.example"}) {
		t.Fatal("preset entries leaked into manual storage")
	}
	if err := c.SetServicePreset("bypass", "test", false); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	got = f.configuration.Routing.Bypass.Domains
	f.mu.Unlock()
	if !reflect.DeepEqual(got, []string{"manual.example"}) {
		t.Fatal("manual rule lost", got)
	}
}

func TestPresetPreviewDoesNotConfigureOrPersist(t *testing.T) {
	t.Setenv("HIKKARI_PRESET_CATALOG_URL", "")
	t.Setenv("HIKKARI_PRESET_EVENTS_URL", "")
	f := &alternateEngine{caps: engine.Capabilities{TUN: true, DomainRouting: true, ProcessRouting: true}}
	c := New()
	c.routingPreview = true
	if err := c.InitWithEngine(t.TempDir(), func(string, engine.Logger) (engine.Backend, error) { return f, nil }); err != nil {
		t.Fatal(err)
	}
	defer c.Shutdown()
	f.mu.Lock()
	before := f.configuration
	f.mu.Unlock()
	if err := c.SetServicePreset("bypass", "steam", true); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	after := f.configuration
	f.mu.Unlock()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("preview touched engine")
	}
	if _, err := os.Stat(filepath.Join(c.BaseDir(), "routing-presets.json")); !os.IsNotExist(err) {
		t.Fatal("preview persisted selections")
	}
}

func TestPresetCommandsAndFailureDoNotPublishSelection(t *testing.T) {
	t.Setenv("HIKKARI_PRESET_CATALOG_URL", "")
	t.Setenv("HIKKARI_PRESET_EVENTS_URL", "")
	f := &alternateEngine{caps: engine.Capabilities{TUN: true, DomainRouting: true, ProcessRouting: true}}
	c := initAlternate(t, f)
	var env Envelope
	if err := json.Unmarshal([]byte(c.Call("RoutingPresets", "")), &env); err != nil || !env.Ok {
		t.Fatal("missing FFI snapshot")
	}
	if err := json.Unmarshal([]byte(c.Call("SetPresetSync", `{"on":false}`)), &env); err != nil || !env.Ok || c.RoutingPresets().SyncEnabled {
		t.Fatal("missing opt-out command")
	}
	f.mu.Lock()
	f.configureErr = fmt.Errorf("rejected")
	f.mu.Unlock()
	if err := c.SetServicePreset("bypass", "steam", true); err == nil {
		t.Fatal("configuration failure swallowed")
	}
	s := c.RoutingPresets()
	if presets.Selected(s.Catalog, s.Choices, "bypass", "steam") {
		t.Fatal("failed selection published")
	}
	f.mu.Lock()
	f.configureErr = nil
	f.mu.Unlock()
	if err := c.presets.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestUserCommandBetweenPresetApplicationAndPublicationKeepsNewRules(t *testing.T) {
	t.Setenv("HIKKARI_PRESET_CATALOG_URL", "")
	t.Setenv("HIKKARI_PRESET_EVENTS_URL", "")
	f := &alternateEngine{caps: engine.Capabilities{DomainRouting: true, ProcessRouting: true}}
	c := initAlternate(t, f)
	injected := false
	m, err := presets.New("", presets.Options{Apply: func(state presets.State) error {
		if err := c.applyPresetState(c.ctx, state); err != nil {
			return err
		}
		// Model the scheduling window after the backend accepted the new
		// group but before the manager can publish/persist its UI snapshot.
		if !injected {
			injected = true
			return c.AddRule("bypass", "domains", "during.example")
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	c.presets = m
	if err := c.SetServicePreset("bypass", "steam", true); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	domains := append([]string{}, f.configuration.Routing.Bypass.Domains...)
	f.mu.Unlock()
	found := map[string]bool{}
	for _, domain := range domains {
		found[domain] = true
	}
	if !found["during.example"] || !found["steampowered.com"] {
		t.Fatal("user command rebuilt an outdated preset snapshot", domains)
	}
}

type failPresetStartEngine struct {
	*alternateEngine
	failOnce atomic.Bool
}

func (f *failPresetStartEngine) Start(ctx context.Context) error {
	if f.failOnce.Swap(false) {
		return fmt.Errorf("new routing failed to start")
	}
	return f.alternateEngine.Start(ctx)
}

func TestFailedPresetRestartRestoresPreviousRunningPolicy(t *testing.T) {
	t.Setenv("HIKKARI_PRESET_CATALOG_URL", "")
	t.Setenv("HIKKARI_PRESET_EVENTS_URL", "")
	f := &failPresetStartEngine{alternateEngine: &alternateEngine{caps: engine.Capabilities{TUN: true, DomainRouting: true, ProcessRouting: true}}}
	c := New()
	if err := c.InitWithEngine(manualRoutingDir(t), func(string, engine.Logger) (engine.Backend, error) { return f, nil }); err != nil {
		t.Fatal(err)
	}
	defer c.Shutdown()
	if _, err := c.AddProfileFromURI("test"); err != nil {
		t.Fatal(err)
	}
	if err := c.StartVPN(); err != nil {
		t.Fatal(err)
	}
	f.failOnce.Store(true)
	if err := c.SetServicePreset("bypass", "steam", true); err == nil {
		t.Fatal("restart failure swallowed")
	}
	if !f.IsRunning() {
		t.Fatal("old working connection was not restored")
	}
	s := c.RoutingPresets()
	if presets.Selected(s.Catalog, s.Choices, "bypass", "steam") {
		t.Fatal("failed selection published")
	}
	f.mu.Lock()
	domains := f.configuration.Routing.Bypass.Domains
	f.mu.Unlock()
	if len(domains) != 0 {
		t.Fatal("failed group still configured", domains)
	}
}
