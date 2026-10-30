package configfile

import (
	"regexp"
	"testing"
)

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

func TestApplyRoutingEmitsFolderRulesAndRealDNS(t *testing.T) {
	cfg := DefaultConfig()
	state := RoutingState{
		Enabled: true,
		Mode:    ModeBypass,
		Bypass:  RouteList{Folders: []string{"steamapps/common/", "bad"}},
	}
	for i := 0; i < 2; i++ { // second pass checks idempotency
		ApplyRouting(cfg, state, SelectorTag)
	}

	var routeRegex, dnsRegex []interface{}
	for _, r := range cfg["route"].(map[string]interface{})["rules"].([]interface{}) {
		if v, ok := r.(map[string]interface{})["process_path_regex"].([]interface{}); ok {
			routeRegex = append(routeRegex, v...)
		}
	}
	for _, r := range cfg["dns"].(map[string]interface{})["rules"].([]interface{}) {
		if v, ok := r.(map[string]interface{})["process_path_regex"].([]interface{}); ok {
			dnsRegex = append(dnsRegex, v...)
		}
	}
	// "bad" has no slash and is dropped by sanitize, never compiled.
	if len(routeRegex) != 1 || len(dnsRegex) != 1 {
		t.Fatalf("want one folder pattern in route and dns, got %v / %v", routeRegex, dnsRegex)
	}
	if _, err := regexp.Compile(routeRegex[0].(string)); err != nil {
		t.Fatalf("emitted pattern does not compile: %v", err)
	}

	state.Enabled = false
	ApplyRouting(cfg, state, SelectorTag)
	for _, r := range cfg["dns"].(map[string]interface{})["rules"].([]interface{}) {
		if isBypassDNSRule(r.(map[string]interface{})) {
			t.Fatalf("bypass dns rule left behind after routing was disabled: %v", r)
		}
	}
}
