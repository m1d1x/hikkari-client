package configfile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRebuildActive_LayoutAndIdempotency(t *testing.T) {
	src, err := os.ReadFile(liveConfigPath)
	if err != nil {
		t.Skipf("live config not available: %v", err)
	}
	tmp := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(tmp, src, 0644); err != nil {
		t.Fatal(err)
	}
	s := New(tmp)

	specs := []ProfileSpec{
		{
			Tag:  "p-main",
			Kind: "endpoint",
			Config: map[string]interface{}{
				"tag": "p-main", "type": "awg",
				"peers": []interface{}{map[string]interface{}{
					"address": "10.0.0.1", "port": 51820,
				}},
			},
		},
		{
			Tag:  "p-hy2",
			Kind: "outbound",
			Config: map[string]interface{}{
				"tag": "p-hy2", "type": "hysteria2",
				"server": "92.246.139.199", "server_port": 443,
			},
		},
	}
	legacy := map[string]bool{"awg-ep": true, "proxy-out": true}

	// Two consecutive rebuilds — output must be stable (no duplicated
	// outbounds, no growing endpoint list, no extra ICMP rules).
	for i := 0; i < 2; i++ {
		if err := s.RebuildActive(specs, "p-hy2", false, legacy); err != nil {
			t.Fatal(err)
		}
	}

	cfg, _ := s.Read()
	eps, _ := cfg["endpoints"].([]interface{})
	if len(eps) != 1 {
		t.Fatalf("endpoints len = %d, want 1", len(eps))
	}

	// Outbounds: direct + p-hy2 + selector.
	obs, _ := cfg["outbounds"].([]interface{})
	tags := map[string]int{}
	var selector map[string]interface{}
	for _, ob := range obs {
		m, _ := ob.(map[string]interface{})
		t, _ := m["tag"].(string)
		tags[t]++
		if t == "proxy" {
			selector = m
		}
	}
	for _, want := range []string{"direct", "p-hy2", "proxy"} {
		if tags[want] != 1 {
			t.Errorf("expected tag %q to appear once, got %d (%v)", want, tags[want], tags)
		}
	}

	if selector == nil {
		t.Fatal("selector missing")
	}
	if d, _ := selector["default"].(string); d != "p-hy2" {
		t.Errorf("selector.default = %q, want p-hy2", d)
	}
	out, _ := selector["outbounds"].([]interface{})
	got, _ := json.Marshal(out)
	if string(got) != `["p-main","p-hy2"]` {
		t.Errorf("selector.outbounds = %s, want [p-main p-hy2]", got)
	}

	// route.final and dns servers' detour follow the selector.
	if v, _ := cfg["route"].(map[string]interface{})["final"].(string); v != "proxy" {
		t.Errorf("route.final = %q, want proxy", v)
	}
	dns, _ := cfg["dns"].(map[string]interface{})
	servers, _ := dns["servers"].([]interface{})
	for _, srv := range servers {
		m, _ := srv.(map[string]interface{})
		// Servers that previously had detour="awg-ep" must now point at proxy.
		if v, _ := m["detour"].(string); v != "" && v != "proxy" {
			t.Errorf("dns.servers[].detour = %q, want proxy", v)
		}
	}

	// ICMP→direct rule must be present exactly once (we have an outbound profile).
	rules, _ := cfg["route"].(map[string]interface{})["rules"].([]interface{})
	icmp := 0
	for _, r := range rules {
		m, _ := r.(map[string]interface{})
		if m["network"] == "icmp" && m["outbound"] == "direct" {
			icmp++
		}
	}
	if icmp != 1 {
		t.Errorf("icmp→direct rule count = %d, want 1", icmp)
	}
}

func TestRebuildActive_DropsLegacyOutbounds(t *testing.T) {
	dir := t.TempDir()
	tmp := filepath.Join(dir, "config.json")
	seed := []byte(`{"endpoints":[],"outbounds":[
	  {"tag":"direct","type":"direct"},
	  {"tag":"proxy-out","type":"hysteria2"},
	  {"tag":"stale-extra","type":"hysteria2"}
	],"route":{"final":"proxy-out","rules":[]}}`)
	if err := os.WriteFile(tmp, seed, 0644); err != nil {
		t.Fatal(err)
	}
	s := New(tmp)
	specs := []ProfileSpec{{
		Tag:    "p-new",
		Kind:   "outbound",
		Config: map[string]interface{}{"tag": "p-new", "type": "hysteria2"},
	}}
	if err := s.RebuildActive(specs, "p-new", false, map[string]bool{"proxy-out": true}); err != nil {
		t.Fatal(err)
	}
	cfg, _ := s.Read()
	obs, _ := cfg["outbounds"].([]interface{})
	for _, ob := range obs {
		m, _ := ob.(map[string]interface{})
		tag, _ := m["tag"].(string)
		if tag == "proxy-out" || tag == "stale-extra" {
			t.Errorf("legacy outbound %q not removed: %+v", tag, obs)
		}
	}
}

func TestRebuildActive_AutoModeAddsUrltestAndRetargets(t *testing.T) {
	dir := t.TempDir()
	tmp := filepath.Join(dir, "config.json")
	seed := []byte(`{"endpoints":[],"outbounds":[{"tag":"direct","type":"direct"}],"route":{"rules":[]}}`)
	if err := os.WriteFile(tmp, seed, 0644); err != nil {
		t.Fatal(err)
	}
	s := New(tmp)
	specs := []ProfileSpec{
		{Tag: "p-a", Kind: "outbound", Config: map[string]interface{}{"tag": "p-a", "type": "hysteria2"}},
		{Tag: "p-b", Kind: "outbound", Config: map[string]interface{}{"tag": "p-b", "type": "vless"}},
	}

	// Auto OFF → route.final must be selector, no urltest in outbounds.
	if err := s.RebuildActive(specs, "p-a", false, nil); err != nil {
		t.Fatal(err)
	}
	cfg, _ := s.Read()
	if v, _ := cfg["route"].(map[string]interface{})["final"].(string); v != "proxy" {
		t.Errorf("auto off: route.final=%q, want proxy", v)
	}
	for _, ob := range cfg["outbounds"].([]interface{}) {
		m, _ := ob.(map[string]interface{})
		if tag, _ := m["tag"].(string); tag == "auto" {
			t.Error("auto off: urltest should not be present")
		}
	}

	// Auto ON → urltest present, route.final = auto.
	if err := s.RebuildActive(specs, "p-a", true, nil); err != nil {
		t.Fatal(err)
	}
	cfg, _ = s.Read()
	if v, _ := cfg["route"].(map[string]interface{})["final"].(string); v != "auto" {
		t.Errorf("auto on: route.final=%q, want auto", v)
	}
	var ut map[string]interface{}
	for _, ob := range cfg["outbounds"].([]interface{}) {
		m, _ := ob.(map[string]interface{})
		if tag, _ := m["tag"].(string); tag == "auto" {
			ut = m
		}
	}
	if ut == nil {
		t.Fatal("auto on: urltest outbound missing")
	}
	if ut["type"] != "urltest" {
		t.Errorf("auto on: type=%q, want urltest", ut["type"])
	}
	if len(ut["outbounds"].([]interface{})) != 2 {
		t.Errorf("auto on: urltest outbounds count = %d, want 2", len(ut["outbounds"].([]interface{})))
	}

	// Flip back to OFF → urltest must disappear, route.final back to proxy.
	if err := s.RebuildActive(specs, "p-a", false, nil); err != nil {
		t.Fatal(err)
	}
	cfg, _ = s.Read()
	if v, _ := cfg["route"].(map[string]interface{})["final"].(string); v != "proxy" {
		t.Errorf("auto off again: route.final=%q, want proxy", v)
	}
	for _, ob := range cfg["outbounds"].([]interface{}) {
		m, _ := ob.(map[string]interface{})
		if tag, _ := m["tag"].(string); tag == "auto" {
			t.Error("auto off again: urltest should have been removed")
		}
	}
}

func TestNeedsSelectorMigration(t *testing.T) {
	dir := t.TempDir()
	tmp := filepath.Join(dir, "config.json")
	legacy := []byte(`{"outbounds":[{"tag":"direct","type":"direct"}]}`)
	if err := os.WriteFile(tmp, legacy, 0644); err != nil {
		t.Fatal(err)
	}
	s := New(tmp)
	if !s.NeedsSelectorMigration() {
		t.Fatal("expected migration needed for legacy config")
	}
	migrated := []byte(`{"outbounds":[{"tag":"direct","type":"direct"},{"tag":"proxy","type":"selector"}]}`)
	if err := os.WriteFile(tmp, migrated, 0644); err != nil {
		t.Fatal(err)
	}
	if s.NeedsSelectorMigration() {
		t.Fatal("expected no migration needed for selector config")
	}
}

// RebuildActive on a machine that has never run the engine: no config.json,
// no routing.json. It must compile the first profile into the DefaultConfig
// skeleton instead of failing with ENOENT.
func TestRebuildActive_FreshInstall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "config.json")
	s := New(path)

	specs := []ProfileSpec{{
		Tag:  "p-germany",
		Kind: "outbound",
		Config: map[string]interface{}{
			"tag": "p-germany", "type": "vless",
			"server": "144.31.104.44", "server_port": 443,
		},
	}}
	if err := s.RebuildActive(specs, "p-germany", false, nil); err != nil {
		t.Fatalf("RebuildActive on fresh install: %v", err)
	}

	cfg, err := s.Read()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg["inbounds"].([]interface{}); !ok {
		t.Error("default skeleton lost its inbounds")
	}
	route, _ := cfg["route"].(map[string]interface{})
	if f, _ := route["final"].(string); f != SelectorTag {
		t.Errorf("route.final = %q, want %q", f, SelectorTag)
	}
	obs, _ := cfg["outbounds"].([]interface{})
	tags := map[string]bool{}
	for _, ob := range obs {
		m, _ := ob.(map[string]interface{})
		tag, _ := m["tag"].(string)
		tags[tag] = true
	}
	for _, want := range []string{"direct", "p-germany", SelectorTag} {
		if !tags[want] {
			t.Errorf("outbound %q missing (got %v)", want, tags)
		}
	}
}

func TestRebuildActive_HealsVoipAndDns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "config.json")
	s := New(path)

	specs := []ProfileSpec{{
		Tag:  "p-germany",
		Kind: "outbound",
		Config: map[string]interface{}{
			"tag": "p-germany", "type": "vless",
			"server": "web.zovfy.ru", "server_port": 443,
		},
	}}
	if err := s.RebuildActive(specs, "p-germany", false, nil); err != nil {
		t.Fatalf("RebuildActive: %v", err)
	}

	cfg, err := s.Read()
	if err != nil {
		t.Fatal(err)
	}

	// Verify DNS
	dns, _ := cfg["dns"].(map[string]interface{})
	if strat, _ := dns["strategy"].(string); strat != "ipv4_only" {
		t.Errorf("dns.strategy = %q, want ipv4_only", strat)
	}
	servers, _ := dns["servers"].([]interface{})
	for _, srv := range servers {
		m, _ := srv.(map[string]interface{})
		tag, _ := m["tag"].(string)
		if tag == "fakeip-dns" {
			if _, hasV6 := m["inet6_range"]; hasV6 {
				t.Error("fakeip-dns should not have inet6_range (causes dual-stack ICE timeouts)")
			}
		}
		if tag == "dns-direct" {
			if typ, _ := m["type"].(string); typ != "local" {
				t.Errorf("dns-direct type = %q, want local", typ)
			}
		}
	}

	// Verify TUN Inbound
	inbounds, _ := cfg["inbounds"].([]interface{})
	if len(inbounds) == 0 {
		t.Fatal("no inbounds found")
	}
	in, _ := inbounds[0].(map[string]interface{})
	var mtu int
	switch v := in["mtu"].(type) {
	case float64:
		mtu = int(v)
	case int:
		mtu = v
	}
	if mtu != 1360 {
		t.Errorf("inbound mtu = %v, want 1360", in["mtu"])
	}
	if ein, _ := in["endpoint_independent_nat"].(bool); !ein {
		t.Error("endpoint_independent_nat should be true for VoIP/games")
	}

	// Verify route.rules sniff port scoping
	route, _ := cfg["route"].(map[string]interface{})
	rules, _ := route["rules"].([]interface{})
	for _, r := range rules {
		m, _ := r.(map[string]interface{})
		if action, _ := m["action"].(string); action == "sniff" {
			ports, _ := m["port"].([]interface{})
			if len(ports) == 0 {
				t.Error("sniff rule must be scoped to web ports so UDP VoIP/game packets are not delayed")
			}
		}
	}
}

