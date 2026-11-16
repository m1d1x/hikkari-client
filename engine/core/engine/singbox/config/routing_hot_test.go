package configfile

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"NekkoVPN/core/presets"
	"NekkoVPN/core/routing"
)

// D3 of plans/2026-11-16-default-mode-blocked-via-vpn.md: switching the
// routing mode changes only what core patch 0016 swaps in place (route.rules,
// route.final, dns.rules). Anything else in the diff makes the core decline
// the reload and restart, and the switch drops the tunnel again.
func TestARoutingModeSwitchChangesOnlyWhatTheCoreSwapsInPlace(t *testing.T) {
	catalog, err := presets.BundledCatalog()
	if err != nil {
		t.Fatal(err)
	}
	store := New(filepath.Join(t.TempDir(), "config.json"))
	compile := func(mode routing.RoutingMode, auto bool) map[string]any {
		t.Helper()
		manual := routing.DefaultRoutingState()
		manual.Mode = mode
		state, err := presets.Effective(manual, catalog, presets.Choices{})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.RebuildActiveWithRouting(fingerprintSpecs(), "p-a", auto, nil, state, HostNetwork{}); err != nil {
			t.Fatal(err)
		}
		read, err := store.Read()
		if err != nil {
			t.Fatal(err)
		}
		// Through JSON, as the core compares the files.
		raw, err := json.Marshal(read)
		if err != nil {
			t.Fatal(err)
		}
		var cfg map[string]any
		if err := json.Unmarshal(raw, &cfg); err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	hot := map[string][]string{"route": {"rules", "final"}, "dns": {"rules"}}
	rest := func(cfg map[string]any) map[string]any {
		out := make(map[string]any, len(cfg))
		for key, value := range cfg {
			section, isMap := value.(map[string]any)
			if !isMap || hot[key] == nil {
				out[key] = value
				continue
			}
			trimmed := make(map[string]any, len(section))
			for sub, v := range section {
				if !slices.Contains(hot[key], sub) {
					trimmed[sub] = v
				}
			}
			out[key] = trimmed
		}
		return out
	}
	for _, auto := range []bool{false, true} {
		bypass, proxy := compile(routing.ModeBypass, auto), compile(routing.ModeProxy, auto)
		if reflect.DeepEqual(bypass["route"], proxy["route"]) {
			t.Fatalf("auto=%v: the modes compiled the same route", auto)
		}
		a, b := rest(bypass), rest(proxy)
		for key := range a {
			if !reflect.DeepEqual(a[key], b[key]) {
				t.Errorf("auto=%v: %s differs between the modes, the core would restart", auto, key)
			}
		}
		// The router searches processes only when it starts with a process
		// rule: both modes need one, or the core declines the switch.
		for name, cfg := range map[string]map[string]any{"bypass": bypass, "proxy": proxy} {
			if !hasProcessRule(cfg) {
				t.Errorf("auto=%v: %s has no process rule, the core cannot take the other mode in place", auto, name)
			}
		}
	}
}

func hasProcessRule(cfg map[string]any) bool {
	route, _ := cfg["route"].(map[string]any)
	rules, _ := route["rules"].([]any)
	for _, r := range rules {
		m, _ := r.(map[string]any)
		if _, has := m["process_name"]; has {
			return true
		}
		if _, has := m["process_path_regex"]; has {
			return true
		}
	}
	return false
}
