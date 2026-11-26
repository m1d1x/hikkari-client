package configfile

import (
	"path/filepath"
	"testing"
)

// Rebuilding an existing config.json must leave the same DNS block as a fresh
// install, or upgraded users would keep faking the names NCSI depends on.
func TestRebuildAppliesRealAddressRuleBeforeFakeIP(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "config.json"))
	spec := ProfileSpec{Tag: "p-a", Kind: kindOutbound, Config: map[string]interface{}{"type": "direct", "tag": "p-a"}}
	if err := store.RebuildActiveWithRouting([]ProfileSpec{spec}, "p-a", false, nil, RoutingState{}, HostNetwork{}); err != nil {
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
