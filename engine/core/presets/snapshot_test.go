package presets

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"NekkoVPN/core/routing"
)

func TestRoutingSnapshot_Roundtrip(t *testing.T) {
	cat, err := BundledCatalog()
	if err != nil {
		t.Fatalf("failed to parse bundled catalog: %v", err)
	}

	manual := routing.DefaultRoutingState()
	manual.Mode = routing.ModeBypass
	manual.Enabled = true

	// Seed manual rules
	if err := manual.ChangeRule(routing.ModeProxy, "domains", "proxy-domain.com", false); err != nil {
		t.Fatalf("failed to add domain rule: %v", err)
	}
	if err := manual.ChangeRule(routing.ModeProxy, "ips", "198.51.100.0/24", false); err != nil {
		t.Fatalf("failed to add ip rule: %v", err)
	}
	if err := manual.ChangeRule(routing.ModeProxy, "processes", "discord.exe", false); err != nil {
		t.Fatalf("failed to add process rule: %v", err)
	}

	if err := manual.ChangeRule(routing.ModeBypass, "domains", "bypass-domain.org", false); err != nil {
		t.Fatalf("failed to add bypass domain: %v", err)
	}
	if err := manual.ChangeRule(routing.ModeBypass, "ips", "203.0.113.1/32", false); err != nil {
		t.Fatalf("failed to add bypass ip: %v", err)
	}

	// Seed choices
	choices := Choices{
		routing.ModeProxy: {
			"discord":  true,
			"telegram": false,
		},
		routing.ModeBypass: {
			"telegram": true,
			"yandex":   true,
			"steam":    false,
		},
	}

	// 1. Build and export snapshot
	snap1 := BuildRoutingSnapshot(manual, cat, choices)
	rawJSON, err := json.MarshalIndent(snap1, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal snapshot: %v", err)
	}

	// 2. Parse snapshot
	parsedSnap, err := ParseRoutingSnapshot(rawJSON)
	if err != nil {
		t.Fatalf("failed to parse snapshot JSON: %v", err)
	}

	// 3. Apply parsed snapshot to clean state
	appliedManual, appliedChoices, err := ApplyRoutingSnapshotToState(parsedSnap, routing.DefaultRoutingState(), cat)
	if err != nil {
		t.Fatalf("failed to apply snapshot: %v", err)
	}

	// 4. Verify 100% identity of manual routing rules
	if appliedManual.Mode != manual.Mode {
		t.Fatalf("mode mismatch: expected %q, got %q", manual.Mode, appliedManual.Mode)
	}
	if !reflect.DeepEqual(appliedManual.Proxy, manual.Proxy) {
		t.Fatalf("proxy rules mismatch:\nexpected: %+v\ngot: %+v", manual.Proxy, appliedManual.Proxy)
	}
	if !reflect.DeepEqual(appliedManual.Bypass, manual.Bypass) {
		t.Fatalf("bypass rules mismatch:\nexpected: %+v\ngot: %+v", manual.Bypass, appliedManual.Bypass)
	}

	// 5. Re-export and verify snapshot equivalence
	snap2 := BuildRoutingSnapshot(appliedManual, cat, appliedChoices)
	if snap1.Version != snap2.Version {
		t.Fatalf("version mismatch: %d != %d", snap1.Version, snap2.Version)
	}
	if snap1.Mode != snap2.Mode {
		t.Fatalf("mode mismatch: %s != %s", snap1.Mode, snap2.Mode)
	}
	if !reflect.DeepEqual(snap1.Groups, snap2.Groups) {
		t.Fatalf("groups mismatch:\nexpected: %+v\ngot: %+v", snap1.Groups, snap2.Groups)
	}
	if !reflect.DeepEqual(snap1.Services, snap2.Services) {
		t.Fatalf("services mismatch:\nexpected: %+v\ngot: %+v", snap1.Services, snap2.Services)
	}
}

func TestRoutingSnapshot_ValidationErrors(t *testing.T) {
	cat, err := BundledCatalog()
	if err != nil {
		t.Fatalf("failed to load catalog: %v", err)
	}

	testCases := []struct {
		name        string
		jsonPayload string
	}{
		{
			name:        "empty string",
			jsonPayload: "",
		},
		{
			name:        "broken json syntax",
			jsonPayload: `{"version": 1, "groups": {`,
		},
		{
			name:        "missing version",
			jsonPayload: `{"groups": {"discord": "proxy"}}`,
		},
		{
			name:        "unsupported version",
			jsonPayload: `{"version": 2, "groups": {"discord": "proxy"}}`,
		},
		{
			name:        "invalid mode",
			jsonPayload: `{"version": 1, "mode": "invalid_mode", "groups": {}}`,
		},
		{
			name:        "invalid group action string",
			jsonPayload: `{"version": 1, "groups": {"discord": "allow"}}`,
		},
		{
			name:        "invalid group action type number",
			jsonPayload: `{"version": 1, "groups": {"discord": 123}}`,
		},
		{
			name:        "invalid group action type bool",
			jsonPayload: `{"version": 1, "groups": {"discord": true}}`,
		},
		{
			name:        "invalid service action string",
			jsonPayload: `{"version": 1, "services": {"example.com": "direct"}}`,
		},
		{
			name:        "invalid service action type array",
			jsonPayload: `{"version": 1, "services": {"example.com": ["proxy"]}}`,
		},
		{
			name:        "groups is not an object",
			jsonPayload: `{"version": 1, "groups": ["discord"]}`,
		},
		{
			name:        "services is not an object",
			jsonPayload: `{"version": 1, "services": "example.com"}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			snap, err := ParseRoutingSnapshot([]byte(tc.jsonPayload))
			if err == nil {
				_, _, applyErr := ApplyRoutingSnapshotToState(snap, routing.DefaultRoutingState(), cat)
				if applyErr == nil {
					t.Fatalf("expected error for case %q, but got nil", tc.name)
				}
			}
		})
	}
}

func TestRoutingSnapshot_CategorizedServices(t *testing.T) {
	cat, err := BundledCatalog()
	if err != nil {
		t.Fatalf("failed to load catalog: %v", err)
	}

	payload := `{
		"version": 1,
		"mode": "proxy",
		"groups": {
			"discord": "proxy",
			"telegram": "bypass"
		},
		"services": {
			"domains": {
				"test-domain.com": "proxy"
			},
			"ips": {
				"10.0.0.0/8": "bypass"
			},
			"processes": {
				"custom_app": "proxy"
			}
		}
	}`

	snap, err := ParseRoutingSnapshot([]byte(payload))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}

	state, choices, err := ApplyRoutingSnapshotToState(snap, routing.DefaultRoutingState(), cat)
	if err != nil {
		t.Fatalf("unexpected apply error: %v", err)
	}

	if state.Mode != routing.ModeProxy {
		t.Fatalf("expected proxy mode, got %s", state.Mode)
	}
	if len(state.Proxy.Domains) != 1 || state.Proxy.Domains[0] != "test-domain.com" {
		t.Fatalf("expected proxy domain test-domain.com, got %v", state.Proxy.Domains)
	}
	if len(state.Bypass.IPs) != 1 || state.Bypass.IPs[0] != "10.0.0.0/8" {
		t.Fatalf("expected bypass ip 10.0.0.0/8, got %v", state.Bypass.IPs)
	}
	if len(state.Proxy.Processes) != 1 || state.Proxy.Processes[0] != "custom_app" {
		t.Fatalf("expected proxy process custom_app, got %v", state.Proxy.Processes)
	}
	if !choices[routing.ModeProxy]["discord"] {
		t.Fatalf("expected discord in proxy choices")
	}
	if !choices[routing.ModeBypass]["telegram"] {
		t.Fatalf("expected telegram in bypass choices")
	}
}

func TestManager_ResetToDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "routing-presets.json")

	m, err := New(path, Options{})
	if err != nil {
		t.Fatalf("failed to initialize manager: %v", err)
	}

	// Change choices away from defaults
	if err := m.Select(routing.ModeBypass, "discord", true); err != nil {
		t.Fatalf("failed to select: %v", err)
	}
	if err := m.Select(routing.ModeProxy, "yandex", true); err != nil {
		t.Fatalf("failed to select: %v", err)
	}

	if len(m.Snapshot().Choices[routing.ModeBypass]) == 0 {
		t.Fatalf("expected non-empty choices")
	}

	// Call ResetToDefaults
	if err := m.ResetToDefaults(); err != nil {
		t.Fatalf("ResetToDefaults failed: %v", err)
	}

	snap := m.Snapshot()
	if len(snap.Choices) != 0 {
		t.Fatalf("expected empty choices after reset, got %+v", snap.Choices)
	}
	if snap.Source != "bundled" {
		t.Fatalf("expected source bundled, got %s", snap.Source)
	}

	// Check that default modes match defaults.json
	for _, s := range snap.Catalog.Services {
		for _, mode := range s.DefaultModes {
			if !Selected(snap.Catalog, snap.Choices, mode, s.ID) {
				t.Fatalf("service %s should be selected for default mode %s", s.ID, mode)
			}
		}
	}
}
