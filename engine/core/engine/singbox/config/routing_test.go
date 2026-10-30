package configfile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// freshCfg returns a config skeleton with the sniff prelude, an ICMP→direct
// rule, and a stale "leftover" rule from a previous compile — the leftover
// must be dropped on every ApplyRouting call.
func freshCfg() map[string]interface{} {
	return map[string]interface{}{
		"route": map[string]interface{}{
			"rules": []interface{}{
				map[string]interface{}{"action": "sniff"},
				map[string]interface{}{"network": "icmp", "outbound": "direct"},
				// Stale leftover — must be removed:
				map[string]interface{}{
					"domain_suffix": []interface{}{"old.example"},
					"outbound":      "direct",
				},
			},
			"final": "proxy",
		},
	}
}

func ruleCount(cfg map[string]interface{}) int {
	r, _ := cfg["route"].(map[string]interface{})
	rules, _ := r["rules"].([]interface{})
	return len(rules)
}

func finalTag(cfg map[string]interface{}) string {
	r, _ := cfg["route"].(map[string]interface{})
	v, _ := r["final"].(string)
	return v
}

func rulesOf(cfg map[string]interface{}) []map[string]interface{} {
	r, _ := cfg["route"].(map[string]interface{})
	raw, _ := r["rules"].([]interface{})
	out := make([]map[string]interface{}, 0, len(raw))
	for _, x := range raw {
		if m, ok := x.(map[string]interface{}); ok {
			out = append(out, m)
		}
	}
	return out
}

func TestApplyRouting_Disabled_ClearsOurRules(t *testing.T) {
	cfg := freshCfg()
	state := RoutingState{Enabled: false, Mode: ModeBypass}
	ApplyRouting(cfg, state, SelectorTag)

	if got, want := ruleCount(cfg), 2; got != want {
		t.Fatalf("rules: got %d, want %d (prelude + icmp only)", got, want)
	}
	if finalTag(cfg) != SelectorTag {
		t.Fatalf("final: got %q, want %q", finalTag(cfg), SelectorTag)
	}
}

func TestApplyRouting_BypassMode_EmitsDirectRules(t *testing.T) {
	cfg := freshCfg()
	state := RoutingState{
		Enabled: true,
		Mode:    ModeBypass,
		Bypass: RouteList{
			Domains:   []string{"example.com"},
			IPs:       []string{"10.0.0.0/8"},
			Processes: []string{"discord.exe"},
		},
	}
	ApplyRouting(cfg, state, SelectorTag)

	if finalTag(cfg) != SelectorTag {
		t.Fatalf("final: got %q, want %q", finalTag(cfg), SelectorTag)
	}
	rules := rulesOf(cfg)
	// prelude + icmp + 3 of ours
	if len(rules) != 5 {
		t.Fatalf("rules: got %d, want 5", len(rules))
	}
	for _, r := range rules[2:] {
		if ob, _ := r["outbound"].(string); ob != "direct" {
			t.Fatalf("rule outbound: got %q, want %q (rule=%v)", ob, "direct", r)
		}
	}
}

func TestApplyRouting_ProxyMode_PointsAtHeadAndFinalDirect(t *testing.T) {
	cfg := freshCfg()
	state := RoutingState{
		Enabled: true,
		Mode:    ModeProxy,
		Proxy: RouteList{
			Domains: []string{"ozon.ru"},
		},
	}
	ApplyRouting(cfg, state, SelectorTag)

	if finalTag(cfg) != "direct" {
		t.Fatalf("final: got %q, want %q", finalTag(cfg), "direct")
	}
	rules := rulesOf(cfg)
	if len(rules) != 3 { // prelude + icmp + 1 ours
		t.Fatalf("rules: got %d, want 3", len(rules))
	}
	last := rules[2]
	if ob, _ := last["outbound"].(string); ob != SelectorTag {
		t.Fatalf("rule outbound: got %q, want %q", ob, SelectorTag)
	}
}

func TestApplyRouting_HonoursAutoModeHead(t *testing.T) {
	cfg := freshCfg()
	state := RoutingState{
		Enabled: true,
		Mode:    ModeProxy,
		Proxy:   RouteList{Domains: []string{"x.com"}},
	}
	ApplyRouting(cfg, state, AutoTag)
	rules := rulesOf(cfg)
	last := rules[len(rules)-1]
	if ob, _ := last["outbound"].(string); ob != AutoTag {
		t.Fatalf("rule outbound: got %q, want %q", ob, AutoTag)
	}
}

func TestApplyRouting_Idempotent(t *testing.T) {
	cfg := freshCfg()
	state := RoutingState{
		Enabled: true,
		Mode:    ModeBypass,
		Bypass:  RouteList{Domains: []string{"a.com"}},
	}
	ApplyRouting(cfg, state, SelectorTag)
	first := ruleCount(cfg)
	ApplyRouting(cfg, state, SelectorTag)
	if ruleCount(cfg) != first {
		t.Fatalf("rule count changed on second apply: %d → %d", first, ruleCount(cfg))
	}
}

func TestStore_RoutingRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"route":{"rules":[]}}`), 0644); err != nil {
		t.Fatal(err)
	}
	s := New(cfgPath)

	if err := s.AddRouteRule(ModeBypass, "domains", "example.com"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddRouteRule(ModeBypass, "domains", "EXAMPLE.com"); err != nil {
		t.Fatal(err) // dedup case-insensitive — must not error
	}
	if err := s.AddRouteRule(ModeProxy, "ips", "1.2.3.4/32"); err != nil {
		t.Fatal(err)
	}

	got, err := s.ReadRouting()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Bypass.Domains) != 1 {
		t.Fatalf("bypass.domains: %v", got.Bypass.Domains)
	}
	if len(got.Proxy.IPs) != 1 || got.Proxy.IPs[0] != "1.2.3.4/32" {
		t.Fatalf("proxy.ips: %v", got.Proxy.IPs)
	}

	// Switching mode should not lose either list.
	if err := s.SetRoutingMode(ModeProxy); err != nil {
		t.Fatal(err)
	}
	got, _ = s.ReadRouting()
	if got.Mode != ModeProxy {
		t.Fatalf("mode: %s", got.Mode)
	}
	if len(got.Bypass.Domains) != 1 || len(got.Proxy.IPs) != 1 {
		t.Fatalf("lists lost on mode switch: %+v", got)
	}

	// And the compiled config.json should now have one rule pointed at
	// the selector tag (proxy mode + ip in the proxy list).
	raw, _ := os.ReadFile(cfgPath)
	var compiled map[string]interface{}
	if err := json.Unmarshal(raw, &compiled); err != nil {
		t.Fatal(err)
	}
	if finalTag(compiled) != "direct" {
		t.Fatalf("compiled final: %s", finalTag(compiled))
	}
	rules := rulesOf(compiled)
	if len(rules) == 0 {
		t.Fatal("compiled: no rules")
	}
	last := rules[len(rules)-1]
	if ob, _ := last["outbound"].(string); ob != SelectorTag {
		t.Fatalf("compiled rule outbound: %s", ob)
	}
}

func TestStore_RemoveRule(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	_ = os.WriteFile(cfgPath, []byte(`{"route":{"rules":[]}}`), 0644)
	s := New(cfgPath)

	_ = s.AddRouteRule(ModeBypass, "domains", "a.com")
	_ = s.AddRouteRule(ModeBypass, "domains", "b.com")
	if err := s.RemoveRouteRule(ModeBypass, "domains", "A.COM"); err != nil {
		t.Fatal(err)
	}
	st, _ := s.ReadRouting()
	if len(st.Bypass.Domains) != 1 || st.Bypass.Domains[0] != "b.com" {
		t.Fatalf("after remove: %v", st.Bypass.Domains)
	}
	// Removing something that isn't there: no error, no change.
	if err := s.RemoveRouteRule(ModeBypass, "domains", "ghost"); err != nil {
		t.Fatal(err)
	}
}

// A bypassed app must see the network as without the VPN: its route rule sits
// ahead of the QUIC reject, and its DNS answers are real, not fake IPs.
func TestApplyRouting_BypassBeatsQUICRejectAndFakeIP(t *testing.T) {
	cfg := DefaultConfig()
	ensureICMPDirectRule(cfg, true)
	state := RoutingState{
		Enabled: true,
		Mode:    ModeBypass,
		Bypass: RouteList{
			Processes: []string{"helldivers2.exe"},
			Domains:   []string{"playfabapi.com"},
		},
	}
	for i := 0; i < 2; i++ { // second pass checks idempotency
		ApplyRouting(cfg, state, SelectorTag)
	}

	rules := cfg["route"].(map[string]interface{})["rules"].([]interface{})
	procAt, rejectAt := -1, -1
	for i, r := range rules {
		m := r.(map[string]interface{})
		if _, ok := m["process_name"]; ok {
			procAt = i
		}
		if isQUICReject(m) {
			rejectAt = i
		}
	}
	if procAt < 0 || rejectAt < 0 || procAt > rejectAt {
		t.Fatalf("process rule at %d, QUIC reject at %d: want process first", procAt, rejectAt)
	}

	dnsRules := cfg["dns"].(map[string]interface{})["rules"].([]interface{})
	n := 0
	for _, r := range dnsRules {
		if m := r.(map[string]interface{}); m["process_name"] != nil && m["server"] == "dns-direct" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("want exactly one process dns-direct rule, got %d", n)
	}
	if m := dnsRules[0].(map[string]interface{}); m["domain_suffix"] == nil || m["server"] != "dns-direct" {
		t.Fatalf("bypassed domains must resolve real, first dns rule is %v", m)
	}
	zovfy := 0
	for _, r := range dnsRules {
		if suf, ok := r.(map[string]interface{})["domain_suffix"].([]interface{}); ok && len(suf) == 1 && suf[0] == "zovfy.ru" {
			zovfy++
		}
	}
	if zovfy != 1 {
		t.Fatalf("the zovfy.ru rule must survive untouched, found %d", zovfy)
	}

	state.Enabled = false
	ApplyRouting(cfg, state, SelectorTag)
	for _, r := range cfg["dns"].(map[string]interface{})["rules"].([]interface{}) {
		if isBypassDNSRule(r.(map[string]interface{})) {
			t.Fatalf("bypass dns rule left behind after routing was disabled: %v", r)
		}
	}
}
