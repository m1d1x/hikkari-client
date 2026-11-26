package configfile

import (
	"path/filepath"
	"testing"
)

// The "авто" button is the auto tunnel. On, a site proven silent on the direct
// path leaves it for the VPN; off, only the lists send traffic to the VPN: in
// the default mode a site that is not listed goes direct, as the person who
// chose that mode expects (vitya-001, 25.11: stake.com, not listed, went to the
// Netherlands exit by itself). A routing change made in between keeps the
// button as it was.
func TestTheAutoButtonAloneSendsSilentSitesThroughTheTunnel(t *testing.T) {
	spec := ProfileSpec{Tag: "p-a", Kind: kindOutbound, Config: map[string]interface{}{"type": "vless", "tag": "p-a"}}
	state := RoutingState{Enabled: true, Mode: ModeProxy, Proxy: RouteList{Domains: []string{"discord.com"}}}
	for _, on := range []bool{true, false} {
		store := New(filepath.Join(t.TempDir(), "config.json"))
		if err := store.RebuildActiveWithRouting([]ProfileSpec{spec}, "p-a", on, nil, state, HostNetwork{}); err != nil {
			t.Fatal(err)
		}
		check := func(when string) {
			t.Helper()
			cfg, err := store.Read()
			if err != nil {
				t.Fatal(err)
			}
			route := cfg["route"].(map[string]interface{})
			if route["final"] != "direct" {
				t.Fatalf("auto=%v %s: final %v", on, when, route["final"])
			}
			if declaresAutoTunnel(cfg) != on {
				t.Fatalf("auto=%v %s: rule set declared %v", on, when, declaresAutoTunnel(cfg))
			}
			tunnelled := ruleIndex(route["rules"].([]interface{}), func(m map[string]interface{}) bool {
				return m["rule_set"] != nil && m["outbound"] == SelectorTag
			})
			if (tunnelled >= 0) != on {
				t.Fatalf("auto=%v %s: silent sites to the VPN at rule %d", on, when, tunnelled)
			}
		}
		check("after a rebuild")
		if err := store.WriteRouting(state); err != nil {
			t.Fatal(err)
		}
		if err := store.SetRoutingEnabled(true); err != nil {
			t.Fatal(err)
		}
		check("after a routing change")
	}
}

// A config written while the button picked the server names its urltest
// groups: the rebuild drops them and points the route back at the selector,
// or the core would refuse a config naming an outbound that is gone.
func TestAConfigOfTheOldServerChoiceComesBackToTheSelector(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "config.json"))
	cfg := DefaultConfig()
	cfg["outbounds"] = []interface{}{
		map[string]interface{}{"tag": "direct", "type": "direct"},
		map[string]interface{}{"tag": "auto", "type": "urltest", "outbounds": []interface{}{"p-a"}},
		map[string]interface{}{"tag": "auto-udp", "type": "urltest", "outbounds": []interface{}{"p-a"}},
	}
	cfg["route"].(map[string]interface{})["final"] = "auto"
	if err := store.Write(cfg); err != nil {
		t.Fatal(err)
	}
	spec := ProfileSpec{Tag: "p-a", Kind: kindOutbound, Config: map[string]interface{}{"type": "vless", "tag": "p-a"}}
	if err := store.RebuildActiveWithRouting([]ProfileSpec{spec}, "p-a", false, nil, RoutingState{}, HostNetwork{}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Read()
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range got["outbounds"].([]interface{}) {
		if tag := raw.(map[string]interface{})["tag"]; tag == "auto" || tag == "auto-udp" {
			t.Fatalf("the old group %v survived", tag)
		}
	}
	if final := got["route"].(map[string]interface{})["final"]; final != SelectorTag {
		t.Fatalf("final %v", final)
	}
}
