package configfile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const liveConfigPath = `C:\Users\CRYBVBY\PROJECTS\NEKOBOX\NekkoVPN\build\bin\config.json`

// TestApplyActive_OutboundRoundtrip exercises the Hysteria2 switch path on a
// copy of the live config.json. Verifies endpoints get cleared, the outbound
// is appended (system outbounds preserved), and route.final / dns.detour are
// re-pointed at the new tag.
func TestApplyActive_OutboundRoundtrip(t *testing.T) {
	src, err := os.ReadFile(liveConfigPath)
	if err != nil {
		t.Skipf("live config not available: %v", err)
	}
	tmp := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(tmp, src, 0644); err != nil {
		t.Fatal(err)
	}
	s := New(tmp)

	activeTags := map[string]bool{"awg-ep": true, "proxy-out": true}
	hy2 := map[string]interface{}{
		"type":        "hysteria2",
		"server":      "92.246.139.199",
		"server_port": 443,
		"password":    "p",
		"tag":         "proxy-out",
	}
	if err := s.ApplyActive("outbound", hy2, "proxy-out", activeTags, "proxy-out"); err != nil {
		t.Fatal(err)
	}

	cfg, err := s.Read()
	if err != nil {
		t.Fatal(err)
	}

	if eps, _ := cfg["endpoints"].([]interface{}); len(eps) != 0 {
		t.Fatalf("endpoints not cleared: %v", eps)
	}

	obs, _ := cfg["outbounds"].([]interface{})
	if len(obs) < 2 {
		t.Fatalf("outbounds too short: %v", obs)
	}
	tags := map[string]bool{}
	for _, o := range obs {
		m, _ := o.(map[string]interface{})
		t, _ := m["tag"].(string)
		tags[t] = true
	}
	if !tags["direct"] {
		t.Errorf("system outbound 'direct' missing after switch: %v", tags)
	}
	if !tags["proxy-out"] {
		t.Errorf("hysteria2 outbound not present: %v", tags)
	}

	if route, _ := cfg["route"].(map[string]interface{}); route != nil {
		if v, _ := route["final"].(string); v != "proxy-out" {
			t.Errorf("route.final = %q, want proxy-out", v)
		}
	}
	if dns, _ := cfg["dns"].(map[string]interface{}); dns != nil {
		servers, _ := dns["servers"].([]interface{})
		for _, srv := range servers {
			m, _ := srv.(map[string]interface{})
			if v, _ := m["detour"].(string); v != "" && v != "proxy-out" {
				t.Errorf("dns.servers[].detour = %q, want proxy-out or empty", v)
			}
		}
	}

	// Switch back to an endpoint — proxy-out should disappear, endpoints
	// repopulate, tags re-point to awg-ep.
	awg := map[string]interface{}{
		"type": "awg",
		"tag":  "awg-ep",
	}
	if err := s.ApplyActive("endpoint", awg, "awg-ep", activeTags, "proxy-out"); err != nil {
		t.Fatal(err)
	}
	cfg2, _ := s.Read()
	for _, o := range cfg2["outbounds"].([]interface{}) {
		m, _ := o.(map[string]interface{})
		if tag, _ := m["tag"].(string); tag == "proxy-out" {
			obs, _ := json.Marshal(cfg2["outbounds"])
			t.Errorf("proxy-out outbound not removed on revert: %s", obs)
		}
	}
	if eps, _ := cfg2["endpoints"].([]interface{}); len(eps) != 1 {
		t.Fatalf("endpoints not repopulated, got %v", eps)
	}
	if v, _ := cfg2["route"].(map[string]interface{})["final"].(string); v != "awg-ep" {
		t.Errorf("route.final after revert = %q, want awg-ep", v)
	}
}

// TestApplyActive_ICMPRule ensures the icmp→direct rule is inserted exactly
// once for outbound profiles and removed on revert to endpoint, regardless
// of how many times we switch.
func TestApplyActive_ICMPRule(t *testing.T) {
	src, err := os.ReadFile(liveConfigPath)
	if err != nil {
		t.Skipf("live config not available: %v", err)
	}
	tmp := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(tmp, src, 0644); err != nil {
		t.Fatal(err)
	}
	s := New(tmp)
	activeTags := map[string]bool{"awg-ep": true, "proxy-out": true}
	hy2 := map[string]interface{}{"type": "hysteria2", "tag": "proxy-out"}
	awg := map[string]interface{}{"type": "awg", "tag": "awg-ep"}

	countICMP := func() int {
		cfg, _ := s.Read()
		route, _ := cfg["route"].(map[string]interface{})
		rules, _ := route["rules"].([]interface{})
		n := 0
		for _, r := range rules {
			m, _ := r.(map[string]interface{})
			net, _ := m["network"].(string)
			ob, _ := m["outbound"].(string)
			if net == "icmp" && ob == "direct" {
				n++
			}
		}
		return n
	}

	// Two consecutive outbound switches — rule must stay at exactly 1.
	for i := 0; i < 2; i++ {
		if err := s.ApplyActive("outbound", hy2, "proxy-out", activeTags, "proxy-out"); err != nil {
			t.Fatal(err)
		}
		if n := countICMP(); n != 1 {
			t.Fatalf("after outbound switch #%d: icmp rule count=%d, want 1", i+1, n)
		}
	}
	// Revert to endpoint — rule must be removed.
	if err := s.ApplyActive("endpoint", awg, "awg-ep", activeTags, "proxy-out"); err != nil {
		t.Fatal(err)
	}
	if n := countICMP(); n != 0 {
		t.Fatalf("after revert: icmp rule count=%d, want 0", n)
	}
}
