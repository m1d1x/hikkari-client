package configfile

import (
	"encoding/json"
	"net/netip"
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

	if got, want := ruleCount(cfg), 3; got != want {
		t.Fatalf("rules: got %d, want %d (prelude + icmp + Russian zone)", got, want)
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
	// prelude + icmp + 3 of ours + 2 for Secure DNS + Russian zone
	if len(rules) != 8 {
		t.Fatalf("rules: got %d, want 8", len(rules))
	}
	for _, r := range rules[2:] {
		if _, secureDNS := r["port"]; secureDNS {
			continue
		}
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
	if len(rules) != 6 { // prelude + icmp + 1 ours + Russian zone + 2 for Secure DNS
		t.Fatalf("rules: got %d, want 6", len(rules))
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

// ruleIndex is the position of the first rule pred accepts, -1 if none.
func ruleIndex(rules []interface{}, pred func(map[string]interface{}) bool) int {
	for i, r := range rules {
		if m, ok := r.(map[string]interface{}); ok && pred(m) {
			return i
		}
	}
	return -1
}

func isSecureDNSTunnelRule(head string) func(map[string]interface{}) bool {
	return func(m map[string]interface{}) bool {
		_, has := m["domain_suffix"]
		ports, _ := m["port"].([]interface{})
		return has && len(ports) == 2 && m["outbound"] == head
	}
}

func isSecureDNSReject(m map[string]interface{}) bool {
	_, has := m["domain_suffix"]
	return has && m["action"] == "reject"
}

func TestSecureDNSStaysInTunnelWhenAppBypasses(t *testing.T) {
	cfg := DefaultConfig()
	route := cfg["route"].(map[string]interface{})
	rules0, _ := route["rules"].([]interface{})
	route["rules"] = append(rules0, map[string]interface{}{"action": "reject", "network": "udp", "port": 443})
	state := RoutingState{
		Enabled: true,
		Mode:    ModeBypass,
		Bypass:  RouteList{Domains: []string{"example.ru"}, Processes: []string{"chrome.exe"}},
	}
	for i := 0; i < 2; i++ { // second pass checks idempotency
		ApplyRouting(cfg, state, SelectorTag)
	}
	rules := cfg["route"].(map[string]interface{})["rules"].([]interface{})

	reject := ruleIndex(rules, isSecureDNSReject)
	tunnel := ruleIndex(rules, isSecureDNSTunnelRule(SelectorTag))
	app := ruleIndex(rules, func(m map[string]interface{}) bool { _, has := m["process_name"]; return has })
	domain := ruleIndex(rules, func(m map[string]interface{}) bool { return m["outbound"] == "direct" && m["domain_suffix"] != nil })
	quic := ruleIndex(rules, isQUICReject)
	if reject < 0 || tunnel < 0 || app < 0 || domain < 0 || quic < 0 {
		t.Fatalf("missing rule: reject=%d tunnel=%d app=%d domain=%d quic=%d in %v", reject, tunnel, app, domain, quic, rules)
	}
	if !(domain < reject && reject < tunnel && tunnel < app && app < quic) {
		t.Fatalf("order: domain=%d reject=%d tunnel=%d app=%d quic=%d", domain, reject, tunnel, app, quic)
	}
	count := 0
	for _, r := range rules {
		if isSecureDNSReject(r.(map[string]interface{})) {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("secure DNS reject compiled %d times, want 1", count)
	}
}

func TestSecureDNSStaysInTunnelWhenEverythingIsDirect(t *testing.T) {
	cfg := DefaultConfig()
	state := RoutingState{Enabled: true, Mode: ModeProxy, Proxy: RouteList{Domains: []string{"youtube.com"}}}
	ApplyRouting(cfg, state, SelectorTag)
	route := cfg["route"].(map[string]interface{})
	if route["final"] != "direct" {
		t.Fatalf("final = %v", route["final"])
	}
	if ruleIndex(route["rules"].([]interface{}), isSecureDNSTunnelRule(SelectorTag)) < 0 {
		t.Fatalf("no secure DNS rule with a direct default: %v", route["rules"])
	}
}

func TestSecureDNSRulesOnlyWhenAnAppCanGoDirect(t *testing.T) {
	for name, state := range map[string]RoutingState{
		"disabled":     {Enabled: false, Mode: ModeBypass, Bypass: RouteList{Processes: []string{"chrome.exe"}}},
		"domains only": {Enabled: true, Mode: ModeBypass, Bypass: RouteList{Domains: []string{"example.ru"}}},
	} {
		cfg := DefaultConfig()
		ApplyRouting(cfg, state, SelectorTag)
		if i := ruleIndex(cfg["route"].(map[string]interface{})["rules"].([]interface{}), isSecureDNSReject); i >= 0 {
			t.Errorf("%s: unexpected secure DNS rule at %d", name, i)
		}
	}
}

func TestAutoTunnelRulesComeBeforeTheAppsAndOnlyWhenDeclared(t *testing.T) {
	dir := t.TempDir()
	store := New(filepath.Join(dir, "config.json"))
	cfg := DefaultConfig()
	if err := store.attachAutoTunnel(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store.AutoTunnelPath()); err != nil {
		t.Fatalf("the core needs the file before it starts: %v", err)
	}
	route := cfg["route"].(map[string]interface{})
	rules0, _ := route["rules"].([]interface{})
	route["rules"] = append(rules0, map[string]interface{}{"action": "reject", "network": "udp", "port": 443})
	state := RoutingState{Enabled: true, Mode: ModeBypass, Bypass: RouteList{Domains: []string{"example.ru"}, Processes: []string{"chrome.exe"}}}
	for i := 0; i < 2; i++ { // second pass checks idempotency
		ApplyRouting(cfg, state, SelectorTag)
	}
	rules := route["rules"].([]interface{})
	isAuto := func(m map[string]interface{}) bool { return m["rule_set"] != nil && m["outbound"] == SelectorTag }
	auto := ruleIndex(rules, isAuto)
	app := ruleIndex(rules, func(m map[string]interface{}) bool { _, has := m["process_name"]; return has })
	domain := ruleIndex(rules, func(m map[string]interface{}) bool { return m["outbound"] == "direct" && m["domain_suffix"] != nil })
	if auto < 0 || !(domain < auto && auto < app) {
		t.Fatalf("order: domain=%d auto=%d app=%d", domain, auto, app)
	}
	count := 0
	for _, r := range rules {
		if m := r.(map[string]interface{}); m["rule_set"] != nil && m["action"] == "reject" {
			count++
			if isQUICReject(m) {
				t.Error("taken for the QUIC reject, it would be preserved across compiles")
			}
		}
	}
	if count != 1 {
		t.Fatalf("QUIC rule compiled %d times", count)
	}

	bare := DefaultConfig()
	ApplyRouting(bare, state, SelectorTag)
	if i := ruleIndex(bare["route"].(map[string]interface{})["rules"].([]interface{}), isAuto); i >= 0 {
		t.Errorf("rule without a declared set at %d: the core would refuse the config", i)
	}
}

// A browser with Secure DNS connects to real IPv6 addresses that neither the
// machine nor the exit may reach. Russian ones are refused before any sniff, the
// rest go by name, in a new config, in an old one healed on rebuild, and through
// every recompile.
func TestRealIPv6RulesRunFirst(t *testing.T) {
	check := func(t *testing.T, cfg map[string]interface{}) {
		t.Helper()
		route, _ := cfg["route"].(map[string]interface{})
		rules, _ := route["rules"].([]interface{})
		if len(rules) < 3 {
			t.Fatalf("%d rules", len(rules))
		}
		russian, _ := rules[0].(map[string]interface{})
		byName, _ := rules[1].(map[string]interface{})
		general, _ := rules[2].(map[string]interface{})
		if !isRussianIPv6Reject(russian) || russian["network"] != "tcp" {
			t.Fatalf("first rule: %v", russian)
		}
		ranges, _ := russian["ip_cidr"].([]interface{})
		yandex := netip.MustParseAddr("2a02:6b8::2:242")
		found := false
		for _, r := range ranges {
			cidr, _ := r.(string)
			prefix := netip.MustParsePrefix(cidr)
			if !prefix.Addr().Is6() {
				t.Fatalf("an IPv4 range among the refused ones: %s", cidr)
			}
			found = found || prefix.Contains(yandex)
		}
		if !found {
			t.Fatalf("Yandex is not among %d refused ranges", len(ranges))
		}
		if on, _ := byName["override_destination"].(bool); !on {
			t.Fatalf("second rule: %v", byName)
		}
		if general["action"] != "sniff" || general["ip_cidr"] != nil {
			t.Fatalf("third rule: %v", general)
		}
		refusals, overrides := 0, 0
		for _, r := range rules {
			m, _ := r.(map[string]interface{})
			if isRussianIPv6Reject(m) {
				refusals++
			}
			if on, _ := m["override_destination"].(bool); on {
				overrides++
			}
		}
		if refusals != 1 || overrides != 1 {
			t.Fatalf("%d refusals, %d overrides", refusals, overrides)
		}
	}
	fresh := DefaultConfig()
	check(t, fresh)
	ApplyRouting(fresh, RoutingState{Enabled: true, Mode: ModeBypass}, SelectorTag)
	check(t, fresh)
	ensureICMPDirectRule(fresh, true)
	check(t, fresh)
	ApplyRouting(fresh, RoutingState{Enabled: true, Mode: ModeBypass}, SelectorTag)
	check(t, fresh)

	old := freshCfg()
	route := old["route"].(map[string]interface{})
	route["rules"] = healRealIPv6Rules(route["rules"].([]interface{}))
	check(t, old)
	route["rules"] = healRealIPv6Rules(route["rules"].([]interface{}))
	check(t, old)

	// A config written by 1.0.41 sniffs the Russian ranges instead of refusing.
	stale := freshCfg()
	route = stale["route"].(map[string]interface{})
	route["rules"] = append([]interface{}{
		map[string]interface{}{"action": "sniff", "ip_cidr": []interface{}{"2a02:6b8::/32"}},
		map[string]interface{}{"action": "sniff", "ip_cidr": []interface{}{"2000::/3"}, "override_destination": true},
	}, route["rules"].([]interface{})...)
	route["rules"] = healRealIPv6Rules(route["rules"].([]interface{}))
	check(t, stale)
}
