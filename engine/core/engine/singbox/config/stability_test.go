package configfile

import (
	"path/filepath"
	"testing"
)

func TestAutoGroupHoldsWorkingNode(t *testing.T) {
	got := autoOutbound([]interface{}{"p-a", "p-b"})
	if got["mode"] != "failover" {
		t.Errorf("mode = %v: least_test would move the exit IP on jitter", got["mode"])
	}
	if _, ok := got["tolerance"]; ok {
		t.Error("tolerance is ignored in failover mode and makes the core warn")
	}
	if v, _ := got["interrupt_exist_connections"].(bool); v {
		t.Error("a move must not cut established connections")
	}
}

// Rebuilding an existing config.json must leave the same DNS block as a fresh
// install, or upgraded users would keep faking the names NCSI depends on.
func TestRebuildAppliesRealAddressRuleBeforeFakeIP(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "config.json"))
	spec := ProfileSpec{Tag: "p-a", Kind: kindOutbound, Config: map[string]interface{}{"type": "direct", "tag": "p-a"}}
	if err := store.RebuildActiveWithRouting([]ProfileSpec{spec}, "p-a", true, nil, RoutingState{}, HostNetwork{}); err != nil {
		t.Fatal(err)
	}
	cfg, err := store.Read()
	if err != nil {
		t.Fatal(err)
	}
	rules, _ := cfg["dns"].(map[string]interface{})["rules"].([]interface{})
	filter, fake := -1, -1
	for i, rule := range rules {
		m := rule.(map[string]interface{})
		if m["server"] == "fakeip-dns" {
			fake = i
		}
		if _, ok := m["domain_regex"]; ok {
			filter = i
			if m["server"] != "dns-direct" {
				t.Errorf("filter resolves through %v", m["server"])
			}
		}
	}
	if filter < 0 || fake < 0 || filter > fake {
		t.Fatalf("filter at %d, fakeip at %d: filter must come first", filter, fake)
	}
}

func TestDialBudgetOnlyWithAFallback(t *testing.T) {
	for _, auto := range []bool{true, false} {
		store := New(filepath.Join(t.TempDir(), "config.json"))
		spec := ProfileSpec{Tag: "p-a", Kind: kindOutbound, Config: map[string]interface{}{
			"type": "vless", "tag": "p-a", "tls": map[string]interface{}{"enabled": true},
		}}
		if err := store.RebuildActiveWithRouting([]ProfileSpec{spec}, "p-a", auto, nil, RoutingState{}, HostNetwork{}); err != nil {
			t.Fatal(err)
		}
		cfg, err := store.Read()
		if err != nil {
			t.Fatal(err)
		}
		var node map[string]interface{}
		for _, raw := range cfg["outbounds"].([]interface{}) {
			if m := raw.(map[string]interface{}); m["tag"] == "p-a" {
				node = m
			}
		}
		_, hasDial := node["connect_timeout"]
		_, hasTLS := node["tls"].(map[string]interface{})["handshake_timeout"]
		if hasDial != auto || hasTLS != auto {
			t.Errorf("auto=%v: connect_timeout=%v handshake_timeout=%v", auto, hasDial, hasTLS)
		}
	}
}
