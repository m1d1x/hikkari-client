package routing

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/document-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestDocumentRoundTripAndPlatformResolution(t *testing.T) {
	d, err := ParseDocument(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := ParseDocument(encoded)
	if err != nil || !reflect.DeepEqual(restored, d) {
		t.Fatalf("round trip: %v", err)
	}
	for platform, executable := range map[string]string{"darwin": "Game", "windows": "game.exe", "linux": "game"} {
		state, err := restored.Resolve(platform)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(state.Bypass.Processes, []string{executable}) {
			t.Fatalf("%s: %v", platform, state.Bypass.Processes)
		}
		if !reflect.DeepEqual(state.Bypass.Domains, []string{"example.com", "games.example"}) {
			t.Fatal(state.Bypass.Domains)
		}
		state.Bypass.Domains[0] = "changed.example"
	}
	if d.Sources[0].Bypass.Domains[0] != "example.com" {
		t.Fatal("resolve mutated source")
	}
	if len(restored.Sources[0].Bypass.Processes) != 3 {
		t.Fatal("foreign OS rules lost")
	}
	// Removing a group preserves its overlapping manual domain.
	restored.Sources = restored.Sources[:1]
	state, err := restored.Resolve("darwin")
	if err != nil || !reflect.DeepEqual(state.Bypass.Domains, []string{"example.com"}) {
		t.Fatalf("manual ownership: %v %v", state, err)
	}
	if _, err := d.Resolve("unknown"); err == nil {
		t.Fatal("unknown OS accepted")
	}
}

func TestDocumentStrictBoundary(t *testing.T) {
	valid := string(fixture(t))
	for name, raw := range map[string]string{
		"case alias":              strings.Replace(valid, `"enabled": true`, `"Enabled": true`, 1),
		"case duplicate":          strings.Replace(valid, `"enabled": true`, `"enabled": true, "Enabled": false`, 1),
		"missing source policy":   strings.Replace(valid, `"proxy": {"domains": [], "ips": [], "processes": {}}`, `"unused": {}`, 1),
		"null source policy":      strings.Replace(valid, `"proxy": {"domains": [], "ips": [], "processes": {}}`, `"proxy": null`, 1),
		"future schema":           strings.Replace(valid, `"schemaVersion": 1`, `"schemaVersion": 2`, 1),
		"unknown field":           strings.Replace(valid, `"enabled": true`, `"enabled": true, "native": {}`, 1),
		"missing enabled":         strings.Replace(valid, `"enabled": true,`, ``, 1),
		"null enabled":            strings.Replace(valid, `"enabled": true`, `"enabled": null`, 1),
		"duplicate":               strings.Replace(valid, `"enabled": true`, `"enabled": true, "enabled": false`, 1),
		"nested duplicate":        strings.Replace(valid, `"id": "manual"`, `"id": "manual", "id": "changed"`, 1),
		"unknown nested":          strings.Replace(valid, `"id": "manual"`, `"id": "manual", "native": true`, 1),
		"duplicate source":        strings.Replace(valid, `"id": "catalog-games"`, `"id": "manual"`, 1),
		"unknown kind":            strings.Replace(valid, `"kind": "manual"`, `"kind": "remote-code"`, 1),
		"unknown mode":            strings.Replace(valid, `"mode": "bypass"`, `"mode": "bad"`, 1),
		"unknown OS":              strings.ReplaceAll(valid, `"darwin"`, `"other"`),
		"URL in file":             strings.ReplaceAll(valid, `"example.com"`, `"https://example.com/path"`),
		"invalid domain":          strings.ReplaceAll(valid, `"example.com"`, `"bad..example"`),
		"unmasked CIDR":           strings.ReplaceAll(valid, `"192.0.2.0/24"`, `"192.0.2.5/24"`),
		"foreign invalid process": strings.ReplaceAll(valid, `"game.exe"`, `"C:\\game.exe"`),
		"trailing":                valid + `{}`,
		"oversize":                strings.Repeat(" ", MaxDocumentBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseDocument([]byte(raw)); err == nil {
				t.Fatal("invalid document accepted")
			}
		})
	}
}

func TestPolicySemanticsAndActiveEquality(t *testing.T) {
	state := DefaultRoutingState()
	state.Bypass.Domains = []string{"direct.example"}
	state.Proxy.IPs = []string{"192.0.2.0/24"}
	p := state.Policy()
	if p.Target != TargetDirect || p.DefaultTarget != TargetVPN || len(p.Rules.Domains) != 1 {
		t.Fatal(p)
	}
	inactive := state
	inactive.Proxy = RouteList{Domains: []string{"inactive.example"}}
	if !ActiveEqual(state, inactive) {
		t.Fatal("inactive edits interrupt VPN")
	}
	state.Mode = ModeProxy
	p = state.Policy()
	if p.Target != TargetVPN || p.DefaultTarget != TargetDirect || len(p.Rules.IPs) != 1 {
		t.Fatal(p)
	}
	if ActiveEqual(state, inactive) {
		t.Fatal("changed active policy ignored")
	}
	state.Enabled = false
	p = state.Policy()
	if p.DefaultTarget != TargetVPN || len(p.Rules.IPs) != 0 {
		t.Fatal(p)
	}
	reordered := DefaultRoutingState()
	reordered.Bypass.Domains = []string{"a.example", "b.example", "a.example"}
	other := reordered
	other.Bypass.Domains = []string{"b.example", "a.example"}
	if !ActiveEqual(reordered, other) {
		t.Fatal("set ordering changes policy")
	}
}
