package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"NekkoVPN/core/engine"
	"NekkoVPN/core/presets"
	"NekkoVPN/core/profile"
	"NekkoVPN/core/routing"
)

// This adapter deliberately accepts a protocol unknown to sing-box and never
// creates a native config, control endpoint or process. The application must
// use only the common engine contract for the complete profile lifecycle.
type alternateEngine struct {
	mu               sync.Mutex
	running          bool
	configuration    engine.Configuration
	selected, probed string
	starts           int
	configureErr     error
	caps             engine.Capabilities
}

func (f *alternateEngine) Info() engine.Info {
	return engine.Info{ID: "alternate", Name: "Alternate test engine", Capabilities: f.caps}
}
func (f *alternateEngine) Configure(c engine.Configuration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.configureErr != nil {
		return f.configureErr
	}
	f.configuration = c
	return nil
}
func (f *alternateEngine) Start(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.running = true
	f.starts++
	return nil
}
func (f *alternateEngine) Stop() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.running = false
	return nil
}
func (f *alternateEngine) IsRunning() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.running }
func (f *alternateEngine) SelectProfile(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.selected = id
	return nil
}
func (f *alternateEngine) Delay(id, url string, timeout int) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.probed = id
	return 42, nil
}
func (f *alternateEngine) Decode(input string) ([]profile.Profile, error) {
	return []profile.Profile{{Label: input, Connection: &profile.Connection{URI: "alternate://opaque", Protocol: "alternate", Host: "127.0.0.1", Port: 9}}}, nil
}
func (f *alternateEngine) Export(p profile.Profile) (string, error)             { return p.Connection.URI, nil }
func (f *alternateEngine) Normalize(p profile.Profile) (profile.Profile, error) { return p, nil }
func (f *alternateEngine) Baseline()                                            {}
func (f *alternateEngine) Diagnose(string)                                      {}

// Contract tests isolate manual policy from the shipped service defaults.
func manualRoutingDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	m, err := presets.New(filepath.Join(dir, "routing-presets.json"), presets.Options{})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := m.Snapshot()
	for _, mode := range []routing.RoutingMode{routing.ModeBypass, routing.ModeProxy} {
		for _, service := range snapshot.Catalog.Services {
			if err := m.Select(mode, service.ID, false); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dir
}

func initAlternate(t *testing.T, f *alternateEngine) *Core {
	t.Helper()
	c := New()
	if err := c.InitWithEngine(manualRoutingDir(t), func(string, engine.Logger) (engine.Backend, error) { return f, nil }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Shutdown() })
	return c
}

func TestApplicationUsesOnlyEngineContract(t *testing.T) {
	f := &alternateEngine{caps: engine.Capabilities{TUN: true, DomainRouting: true, IPRouting: true, AutoSelect: true, LiveProfileSwitch: true, ProfileProbe: true, ProcessRouting: true}}
	c := initAlternate(t, f)
	if c.EngineInfo().ID != "alternate" {
		t.Fatal("adapter identity was not exposed")
	}
	first, err := c.AddProfileFromURI("p-first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.AddProfileFromURI("second")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SwitchProfile(first); err != nil {
		t.Fatal(err)
	}
	if err := c.StartVPN(); err != nil {
		t.Fatal(err)
	}
	if !c.IsRunning() {
		t.Fatal("running state came from a different engine")
	}
	if err := c.SwitchProfile(second); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	selected := f.selected
	f.mu.Unlock()
	if selected != second {
		t.Fatalf("native tag leaked to adapter: %q", selected)
	}
	results := c.PingServices(first, []ServicePingTarget{{Name: "Test", URL: "https://example.test/?a=1&b=2"}})
	if len(results) != 1 || !results[0].OK || results[0].RttMs != 42 {
		t.Fatal("service probe bypassed adapter")
	}
	f.mu.Lock()
	probed := f.probed
	f.mu.Unlock()
	if probed != first {
		t.Fatalf("profile ID starting with p- was incorrectly changed: %q", probed)
	}
	if err := c.AddRule("bypass", "domains", "example.test"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetAutoMode(true); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	config := f.configuration
	f.mu.Unlock()
	if !config.AutoSelect || len(config.Routing.Bypass.Domains) != 1 || len(config.Profiles) != 2 {
		t.Fatal("shared user configuration did not reach adapter")
	}
	if config.Profiles[0].Config != nil || config.Profiles[0].Connection.URI != "alternate://opaque" {
		t.Fatal("portable profile contains native config")
	}
	if _, err := os.Stat(filepath.Join(c.BaseDir(), "config.json")); !os.IsNotExist(err) {
		t.Fatal("application generated a native config for an unrelated engine")
	}
	if uri, err := c.ExportProfileURI(first); err != nil || !strings.HasPrefix(uri, "alternate://opaque") {
		t.Fatal("profile export bypassed adapter")
	}
	if err := c.DeleteProfile(second); err != nil {
		t.Fatal(err)
	}
	if active, _ := c.ActiveProfile(); active != first {
		t.Fatal("active deletion did not select remaining profile")
	}
	if err := c.DeleteProfile(first); err != nil {
		t.Fatal(err)
	}
	if c.IsRunning() {
		t.Fatal("deleting last profile restarted the engine")
	}
	if profiles, _ := c.Profiles(); len(profiles) != 0 {
		t.Fatal("last profile was not removed")
	}
}

func TestEngineCapabilitiesRejectUnsupportedCommands(t *testing.T) {
	c := initAlternate(t, &alternateEngine{})
	if err := c.SetAutoMode(true); err == nil {
		t.Fatal("unsupported auto-select was accepted")
	}
	if on, _ := c.AutoMode(); on {
		t.Fatal("unsupported command changed persisted preferences")
	}
	if err := c.AddRule("bypass", "processes", "example.exe"); err == nil {
		t.Fatal("unsupported process routing was accepted")
	}
}

func TestEngineConfigurationFailurePreventsStart(t *testing.T) {
	f := &alternateEngine{}
	c := initAlternate(t, f)
	if _, err := c.AddProfileFromURI("Server"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.configureErr = errors.New("cannot compile configuration")
	f.mu.Unlock()
	if err := c.StartVPN(); err == nil || !strings.Contains(err.Error(), "cannot compile") {
		t.Fatalf("compile failure was ignored: %v", err)
	}
	f.mu.Lock()
	starts := f.starts
	f.mu.Unlock()
	if starts != 0 {
		t.Fatal("engine started with stale configuration")
	}
}

func TestPersistedUnsupportedProcessPolicyCannotStart(t *testing.T) {
	f := &alternateEngine{}
	c := initAlternate(t, f)
	if _, err := c.AddProfileFromURI("Server"); err != nil {
		t.Fatal(err)
	}
	policy, _ := c.Routing()
	policy.Bypass.Processes = []string{"Browser"}
	if err := c.routing.WriteRouting(policy); err != nil {
		t.Fatal(err)
	}
	// Persistence is read at initialization; simulate an actual app restart.
	dir := c.baseDir
	if err := c.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if err := c.InitWithEngine(dir, func(string, engine.Logger) (engine.Backend, error) { return f, nil }); err != nil {
		t.Fatal(err)
	}
	if err := c.StartVPN(); err == nil || !strings.Contains(err.Error(), "приложений") {
		t.Fatalf("persisted rule silently ignored: %v", err)
	}
	if c.IsRunning() {
		t.Fatal("started despite unsupported policy")
	}
	policy.Enabled = false
	if err := c.routing.WriteRouting(policy); err != nil {
		t.Fatal(err)
	}
	if err := c.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if err := c.InitWithEngine(dir, func(string, engine.Logger) (engine.Backend, error) { return f, nil }); err != nil {
		t.Fatal(err)
	}
	if err := c.SetRoutingEnabled(true); err == nil {
		t.Fatal("enabled unsupported saved policy")
	}
	saved, _ := c.Routing()
	if saved.Enabled || len(saved.Bypass.Processes) != 1 {
		t.Fatal("failed command changed saved policy")
	}
}

func TestTransportWithoutTUNRequiresRoutingFrontend(t *testing.T) {
	c := initAlternate(t, &alternateEngine{})
	if _, err := c.AddProfileFromURI("Server"); err != nil {
		t.Fatal(err)
	}
	if err := c.StartVPN(); err == nil || !strings.Contains(err.Error(), "TUN") {
		t.Fatalf("missing routing frontend ignored: %v", err)
	}
	if c.IsRunning() {
		t.Fatal("started transport without TUN frontend")
	}
}

func TestExplicitInitDoesNotSilentlyChangeEngine(t *testing.T) {
	c := initAlternate(t, &alternateEngine{})
	if err := c.InitForEngine(c.BaseDir(), "xray"); err == nil {
		t.Fatal("engine switch was silently ignored on a live Core")
	}
	if c.EngineInfo().ID != "alternate" {
		t.Fatal("existing adapter changed")
	}
}
