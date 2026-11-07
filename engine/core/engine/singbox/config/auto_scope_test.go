package configfile

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestAutoExcludesUnrelatedProfilesWithoutRemovingManualChoices(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "config.json"))
	specs := []ProfileSpec{
		{Tag: "ours", Kind: kindOutbound, Config: map[string]interface{}{"type": "direct", "tag": "ours"}},
		{Tag: "foreign", Kind: kindOutbound, Config: map[string]interface{}{"type": "direct", "tag": "foreign"}, ExcludeFromAuto: true},
	}
	if err := store.RebuildActive(specs, "foreign", true, nil); err != nil {
		t.Fatal(err)
	}
	cfg, err := store.Read()
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range cfg["outbounds"].([]interface{}) {
		out := raw.(map[string]interface{})
		switch out["tag"] {
		case AutoTag:
			if !reflect.DeepEqual(out["outbounds"], []interface{}{"ours"}) {
				t.Fatalf("foreign profile entered auto: %v", out)
			}
		case SelectorTag:
			if len(out["outbounds"].([]interface{})) != 2 {
				t.Fatal("manual choices were removed")
			}
		}
	}
	// Admission may reject the last eligible profile: never fall back to foreign.
	if err := store.RebuildActive(specs[1:], "foreign", true, nil); err == nil {
		t.Fatal("empty auto scope silently used another provider")
	}
}
