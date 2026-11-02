package api

import (
	"encoding/json"
	"reflect"
	"testing"

	"NekkoVPN/core/engine"
	"NekkoVPN/core/presets"
	"NekkoVPN/core/routing"
)

func TestCore_RoutingSnapshot_Roundtrip(t *testing.T) {
	f := &alternateEngine{caps: engine.Capabilities{TUN: true, DomainRouting: true, IPRouting: true, ProcessRouting: true, FolderRouting: true}}
	c := initAlternate(t, f)

	// Set custom routing mode and manual rules
	if err := c.SetRoutingMode("bypass"); err != nil {
		t.Fatalf("SetRoutingMode failed: %v", err)
	}
	if err := c.AddRule("proxy", "domains", "service-test.org"); err != nil {
		t.Fatalf("AddRule proxy domain failed: %v", err)
	}
	if err := c.AddRule("bypass", "ips", "192.168.1.0/24"); err != nil {
		t.Fatalf("AddRule bypass ip failed: %v", err)
	}
	if err := c.AddRule("proxy", "processes", "discord.exe"); err != nil {
		t.Fatalf("AddRule proxy process failed: %v", err)
	}

	// Change some service presets
	if err := c.SetServicePreset("proxy", "telegram", true); err != nil {
		t.Fatalf("SetServicePreset failed: %v", err)
	}
	if err := c.SetServicePreset("bypass", "telegram", false); err != nil {
		t.Fatalf("SetServicePreset failed: %v", err)
	}
	if err := c.SetServicePreset("proxy", "yandex", true); err != nil {
		t.Fatalf("SetServicePreset failed: %v", err)
	}

	// Flush routing
	if err := c.FlushRouting(); err != nil {
		t.Fatalf("FlushRouting failed: %v", err)
	}

	// 1. Export snapshot
	exportedJSON, err := c.ExportRoutingSnapshot()
	if err != nil {
		t.Fatalf("ExportRoutingSnapshot failed: %v", err)
	}

	var parsed1 presets.RoutingSnapshot
	if err := json.Unmarshal([]byte(exportedJSON), &parsed1); err != nil {
		t.Fatalf("exported JSON is invalid: %v", err)
	}
	if parsed1.Version != 1 {
		t.Fatalf("expected version 1, got %d", parsed1.Version)
	}
	if parsed1.Mode != "bypass" {
		t.Fatalf("expected mode bypass, got %s", parsed1.Mode)
	}
	if parsed1.Services["service-test.org"] != "proxy" {
		t.Fatalf("expected proxy domain in snapshot services, got %v", parsed1.Services)
	}
	if parsed1.Services["192.168.1.0/24"] != "bypass" {
		t.Fatalf("expected bypass ip in snapshot services, got %v", parsed1.Services)
	}

	// 2. Change state to something else completely
	if err := c.ResetToDefaults(); err != nil {
		t.Fatalf("ResetToDefaults failed: %v", err)
	}
	resetManual, _ := c.manualRouting()
	if len(resetManual.Proxy.Domains) != 0 || len(resetManual.Bypass.IPs) != 0 {
		t.Fatalf("state was not cleared after reset: %+v", resetManual)
	}

	// 3. Import snapshot back
	if err := c.ImportRoutingSnapshot(exportedJSON); err != nil {
		t.Fatalf("ImportRoutingSnapshot failed: %v", err)
	}

	// 4. Verify roundtrip: manual rules are 100% restored
	restoredManual, err := c.manualRouting()
	if err != nil {
		t.Fatalf("manualRouting failed: %v", err)
	}
	if restoredManual.Mode != routing.ModeBypass {
		t.Fatalf("expected bypass mode, got %s", restoredManual.Mode)
	}
	if !reflect.DeepEqual(restoredManual.Proxy.Domains, []string{"service-test.org"}) {
		t.Fatalf("restored proxy domains mismatch: got %v", restoredManual.Proxy.Domains)
	}
	if !reflect.DeepEqual(restoredManual.Bypass.IPs, []string{"192.168.1.0/24"}) {
		t.Fatalf("restored bypass ips mismatch: got %v", restoredManual.Bypass.IPs)
	}
	if !reflect.DeepEqual(restoredManual.Proxy.Processes, []string{"discord.exe"}) {
		t.Fatalf("restored proxy processes mismatch: got %v", restoredManual.Proxy.Processes)
	}

	// 5. Re-export snapshot and compare with parsed1
	exportedJSON2, err := c.ExportRoutingSnapshot()
	if err != nil {
		t.Fatalf("second ExportRoutingSnapshot failed: %v", err)
	}
	var parsed2 presets.RoutingSnapshot
	if err := json.Unmarshal([]byte(exportedJSON2), &parsed2); err != nil {
		t.Fatalf("second exported JSON is invalid: %v", err)
	}
	if parsed1.Version != parsed2.Version {
		t.Fatalf("version mismatch: %d != %d", parsed1.Version, parsed2.Version)
	}
	if parsed1.Mode != parsed2.Mode {
		t.Fatalf("mode mismatch: %s != %s", parsed1.Mode, parsed2.Mode)
	}
	if !reflect.DeepEqual(parsed1.Groups, parsed2.Groups) {
		t.Fatalf("groups mismatch:\nexpected: %+v\ngot: %+v", parsed1.Groups, parsed2.Groups)
	}
	if !reflect.DeepEqual(parsed1.Services, parsed2.Services) {
		t.Fatalf("services mismatch:\nexpected: %+v\ngot: %+v", parsed1.Services, parsed2.Services)
	}
}

func TestCore_RoutingSnapshot_RejectsInvalidWithoutModifyingState(t *testing.T) {
	f := &alternateEngine{caps: engine.Capabilities{TUN: true, DomainRouting: true, IPRouting: true, ProcessRouting: true, FolderRouting: true}}
	c := initAlternate(t, f)

	if err := c.AddRule("proxy", "domains", "original.com"); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}
	if err := c.FlushRouting(); err != nil {
		t.Fatalf("FlushRouting failed: %v", err)
	}

	initialState, _ := c.manualRouting()
	initialSnapshot, _ := c.ExportRoutingSnapshot()

	invalidPayloads := []struct {
		name string
		data string
	}{
		{"empty", ""},
		{"malformed json", `{"version": 1, "groups": `},
		{"unsupported version", `{"version": 99, "groups": {}}`},
		{"invalid group value", `{"version": 1, "groups": {"discord": "unsupported_action"}}`},
		{"invalid service value", `{"version": 1, "services": {"foo.com": "invalid_action"}}`},
		{"invalid rule syntax", `{"version": 1, "services": {"invalid domain with spaces.com": "proxy"}}`},
	}

	for _, tc := range invalidPayloads {
		t.Run(tc.name, func(t *testing.T) {
			err := c.ImportRoutingSnapshot(tc.data)
			if err == nil {
				t.Fatalf("expected error for case %q, but got nil", tc.name)
			}
			// Verify state is completely unchanged
			currentState, _ := c.manualRouting()
			if !reflect.DeepEqual(initialState, currentState) {
				t.Fatalf("state was modified on failure %q: before=%+v, after=%+v", tc.name, initialState, currentState)
			}
			currentSnapshot, _ := c.ExportRoutingSnapshot()
			var s1, s2 presets.RoutingSnapshot
			_ = json.Unmarshal([]byte(initialSnapshot), &s1)
			_ = json.Unmarshal([]byte(currentSnapshot), &s2)
			if !reflect.DeepEqual(s1.Groups, s2.Groups) || !reflect.DeepEqual(s1.Services, s2.Services) {
				t.Fatalf("snapshot was modified on failure %q", tc.name)
			}
		})
	}
}

func TestCore_ResetToDefaults(t *testing.T) {
	f := &alternateEngine{caps: engine.Capabilities{TUN: true, DomainRouting: true, IPRouting: true, ProcessRouting: true, FolderRouting: true}}
	c := initAlternate(t, f)

	// Add manual rules
	if err := c.AddRule("proxy", "domains", "custom-manual.com"); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}
	if err := c.AddRule("bypass", "ips", "10.1.2.3/32"); err != nil {
		t.Fatalf("AddRule failed: %v", err)
	}
	if err := c.SetRoutingMode("proxy"); err != nil {
		t.Fatalf("SetRoutingMode failed: %v", err)
	}
	if err := c.SetServicePreset("bypass", "discord", true); err != nil {
		t.Fatalf("SetServicePreset failed: %v", err)
	}
	if err := c.FlushRouting(); err != nil {
		t.Fatalf("FlushRouting failed: %v", err)
	}

	// Now call ResetToDefaults
	if err := c.ResetToDefaults(); err != nil {
		t.Fatalf("ResetToDefaults failed: %v", err)
	}

	manual, err := c.manualRouting()
	if err != nil {
		t.Fatalf("manualRouting failed: %v", err)
	}

	// 1. Manual rules must be empty and mode must be default (bypass)
	if manual.Mode != routing.ModeBypass {
		t.Fatalf("expected default mode bypass, got %s", manual.Mode)
	}
	if len(manual.Proxy.Domains)+len(manual.Proxy.IPs)+len(manual.Proxy.Processes)+len(manual.Proxy.Folders) != 0 {
		t.Fatalf("expected empty proxy rules, got %+v", manual.Proxy)
	}
	if len(manual.Bypass.Domains)+len(manual.Bypass.IPs)+len(manual.Bypass.Processes)+len(manual.Bypass.Folders) != 0 {
		t.Fatalf("expected empty bypass rules, got %+v", manual.Bypass)
	}

	// 2. Exported snapshot must match defaults.json baseline
	exported, err := c.ExportRoutingSnapshot()
	if err != nil {
		t.Fatalf("ExportRoutingSnapshot failed: %v", err)
	}
	var snap presets.RoutingSnapshot
	if err := json.Unmarshal([]byte(exported), &snap); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(snap.Services) != 0 {
		t.Fatalf("expected empty services after reset, got %+v", snap.Services)
	}

	bundledCat, _ := presets.BundledCatalog()
	for _, s := range bundledCat.Services {
		for _, m := range s.DefaultModes {
			expectedAction := string(m)
			if snap.Groups[s.ID] != expectedAction {
				t.Fatalf("service %q expected default action %q, got %q", s.ID, expectedAction, snap.Groups[s.ID])
			}
		}
	}
}

func TestCore_Dispatch_RoutingSnapshot(t *testing.T) {
	f := &alternateEngine{caps: engine.Capabilities{TUN: true, DomainRouting: true, IPRouting: true, ProcessRouting: true, FolderRouting: true}}
	c := initAlternate(t, f)

	// Call ExportRoutingSnapshot via JSON dispatch
	res := c.Call("ExportRoutingSnapshot", "")
	var env1 struct {
		Ok   bool   `json:"ok"`
		Data string `json:"data"`
	}
	if err := json.Unmarshal([]byte(res), &env1); err != nil || !env1.Ok {
		t.Fatalf("ExportRoutingSnapshot dispatch failed: %s", res)
	}

	// Call ImportRoutingSnapshot with invalid JSON via dispatch
	resErr := c.Call("ImportRoutingSnapshot", `{"data":"{invalid"}`)
	var envErr struct {
		Ok    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(resErr), &envErr); err != nil || envErr.Ok || envErr.Error == "" {
		t.Fatalf("expected dispatch error for invalid import: %s", resErr)
	}

	// Call ResetToDefaults via dispatch
	resReset := c.Call("ResetToDefaults", "")
	var envReset struct {
		Ok bool `json:"ok"`
	}
	if err := json.Unmarshal([]byte(resReset), &envReset); err != nil || !envReset.Ok {
		t.Fatalf("ResetToDefaults dispatch failed: %s", resReset)
	}
}
