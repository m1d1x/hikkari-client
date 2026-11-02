package diag

import (
	"encoding/json"
	"testing"

	config "NekkoVPN/core/engine/singbox/config"
)

// The Russian zone's thousands of addresses must not push the rules after them
// off the 8 KB log line.
func TestTheLoggedConfigFitsItsLine(t *testing.T) {
	cfg := config.DefaultConfig()
	config.ApplyRouting(cfg, config.RoutingState{Enabled: false, Mode: config.ModeBypass}, config.SelectorTag)
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var v interface{}
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(abridge(v))
	if len(out) > 8<<10 {
		t.Fatalf("logged config is %d bytes, the line holds 8 KB", len(out))
	}
	short := abridge([]interface{}{"a", "b"}).([]interface{})
	if len(short) != 2 {
		t.Fatalf("a short list was abridged: %v", short)
	}
}
