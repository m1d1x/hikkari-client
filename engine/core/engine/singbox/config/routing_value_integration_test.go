package configfile

import "testing"

func TestApplyRoutingEmitsOnlyParseableIPCIDR(t *testing.T) {
	// End to end: the poisoned value must not reach route.rules, because
	// that JSON is what sing-box refuses to start on.
	state := RoutingState{
		Enabled: true,
		Mode:    ModeProxy,
		Proxy: RouteList{
			IPs: []string{"https://boosty.to/ueyo", "10.0.0.0/8"},
		},
	}
	cfg := map[string]interface{}{}
	ApplyRouting(cfg, state, SelectorTag)

	route := cfg["route"].(map[string]interface{})
	rules := route["rules"].([]interface{})
	var seen []interface{}
	for _, r := range rules {
		m := r.(map[string]interface{})
		if v, ok := m["ip_cidr"]; ok {
			seen = v.([]interface{})
		}
	}
	if len(seen) != 1 || seen[0] != "10.0.0.0/8" {
		t.Fatalf("ip_cidr rule got %v, want [10.0.0.0/8]", seen)
	}
}
