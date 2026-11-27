package configfile

import (
	"testing"
)

func isListQUICReject(r map[string]interface{}, key string) bool {
	_, has := r[key]
	ports, _ := r["port"].([]interface{})
	return has && r["action"] == "reject" && r["network"] == "udp" && len(ports) == 1 && ports[0] == 443
}

func cfgWithQUICReject() map[string]interface{} {
	cfg := freshCfg()
	cfg["dns"] = canonicalDNS()
	ensureICMPDirectRule(cfg, true)
	return cfg
}

func indexOf(rules []map[string]interface{}, match func(map[string]interface{}) bool) int {
	for i, r := range rules {
		if match(r) {
			return i
		}
	}
	return -1
}

func isDirectUDP443(r map[string]interface{}) bool {
	ports, _ := r["port"].([]interface{})
	_, named := r["domain_suffix"]
	return r["outbound"] == "direct" && r["network"] == "udp" && len(ports) == 1 && ports[0] == 443 && !named
}

// In proxy mode what nobody listed goes direct and must see the network as
// without the VPN: UDP 443 passes, tunnelled lists keep their own reject.
// dima-002, 27.11: Helldivers 2 could not reach other players in the default
// mode while every rule of his was off.
func TestProxyModeLetsUnlistedUDP443GoDirect(t *testing.T) {
	cfg := cfgWithQUICReject()
	state := RoutingState{Enabled: true, Mode: ModeProxy, Proxy: RouteList{
		Domains:   []string{"discord.com"},
		Processes: []string{"Discord.exe"},
	}}
	ApplyRouting(cfg, state, SelectorTag)
	rules := rulesOf(cfg)

	global := indexOf(rules, isQUICReject)
	direct443 := indexOf(rules, isDirectUDP443)
	if global < 0 || direct443 < 0 || direct443 > global {
		t.Fatalf("UDP 443 direct at %d, global reject at %d: want direct first (%v)", direct443, global, rules)
	}
	for _, key := range []string{"domain_suffix", "process_name"} {
		reject := indexOf(rules, func(r map[string]interface{}) bool { return isListQUICReject(r, key) })
		route := indexOf(rules, func(r map[string]interface{}) bool {
			_, has := r[key]
			return has && r["outbound"] == SelectorTag
		})
		if reject < 0 || route < 0 || reject+1 != route {
			t.Fatalf("%s: reject at %d, route at %d: want the reject right before the route", key, reject, route)
		}
		if route > direct443 {
			t.Fatalf("%s: the tunnelled list at %d must come before UDP 443 direct at %d", key, route, direct443)
		}
	}
	if finalTag(cfg) != "direct" {
		t.Fatalf("final %q", finalTag(cfg))
	}
}

// With the tunnel as the default nothing changes: the bypass list goes ahead
// of the global reject, which catches the tunnelled rest, and no UDP 443 rule
// of ours goes direct.
func TestBypassModeKeepsTheGlobalQUICReject(t *testing.T) {
	cfg := cfgWithQUICReject()
	state := RoutingState{Enabled: true, Mode: ModeBypass, Bypass: RouteList{Processes: []string{"helldivers2.exe"}}}
	ApplyRouting(cfg, state, SelectorTag)
	rules := rulesOf(cfg)
	global := indexOf(rules, isQUICReject)
	game := indexOf(rules, func(r map[string]interface{}) bool { _, has := r["process_name"]; return has })
	if global < 0 || game < 0 || game > global {
		t.Fatalf("game at %d, reject at %d", game, global)
	}
	for _, r := range rules {
		if isListQUICReject(r, "process_name") {
			t.Fatalf("a direct list needs no reject of its own: %v", r)
		}
		if isDirectUDP443(r) {
			t.Fatalf("bypass mode sends no UDP 443 direct by default: %v", r)
		}
	}
	for _, r := range dnsRulesOf(cfg) {
		if isProxyModeDNSRule(r) {
			t.Fatalf("bypass mode keeps fake answers for the tunnel: %v", r)
		}
	}
}

func dnsRulesOf(cfg map[string]interface{}) []map[string]interface{} {
	dns, _ := cfg["dns"].(map[string]interface{})
	rules, _ := dns["rules"].([]interface{})
	out := make([]map[string]interface{}, 0, len(rules))
	for _, r := range rules {
		if m, ok := r.(map[string]interface{}); ok {
			out = append(out, m)
		}
	}
	return out
}

// Fake answers only for what goes through the tunnel; the rest of the TUN
// queries get real ones, ahead of the catch-all fake rule and behind the
// empty answer for non-A/AAAA queries.
func TestProxyModeFakesOnlyTunnelledNames(t *testing.T) {
	cfg := cfgWithQUICReject()
	route := cfg["route"].(map[string]interface{})
	route["rule_set"] = []interface{}{map[string]interface{}{"tag": AutoTunnelTag}}
	state := RoutingState{Enabled: true, Mode: ModeProxy, Proxy: RouteList{Domains: []string{"discord.com"}}}
	for i := 0; i < 2; i++ { // the second pass checks idempotency
		ApplyRouting(cfg, state, SelectorTag)
	}
	rules := dnsRulesOf(cfg)

	listed := indexOf(rules, func(r map[string]interface{}) bool {
		suf, _ := r["domain_suffix"].([]interface{})
		return r["server"] == "fakeip-dns" && len(suf) == 1 && suf[0] == "discord.com"
	})
	auto := indexOf(rules, func(r map[string]interface{}) bool {
		_, set := r["rule_set"]
		return set && r["server"] == "fakeip-dns"
	})
	secure := indexOf(rules, func(r map[string]interface{}) bool {
		suf, _ := r["domain_suffix"].([]interface{})
		return r["server"] == "fakeip-dns" && len(suf) == len(secureDNSDomains)
	})
	real := indexOf(rules, func(r map[string]interface{}) bool {
		_, in := r["inbound"]
		return in && r["server"] == "dns-direct"
	})
	fake := indexOf(rules, isFakeIPCatchAll)
	empty := indexOf(rules, func(r map[string]interface{}) bool { return r["action"] == "predefined" })
	if listed < 0 || auto < 0 || secure < 0 || real < 0 || fake < 0 || empty < 0 {
		t.Fatalf("listed %d auto %d secure %d real %d fake %d empty %d: %v", listed, auto, secure, real, fake, empty, rules)
	}
	if !(empty < listed && listed < real && auto < real && secure < real && real < fake) {
		t.Fatalf("order: empty %d, listed %d, auto %d, secure %d, real %d, fake %d", empty, listed, auto, secure, real, fake)
	}
	count := 0
	for _, r := range rules {
		if isProxyModeDNSRule(r) {
			count++
		}
	}
	if count != 4 {
		t.Fatalf("%d rules of ours after two compiles, want 4", count)
	}

	// Back to bypass: the fake catch-all is the default again.
	ApplyRouting(cfg, RoutingState{Enabled: true, Mode: ModeBypass}, SelectorTag)
	for _, r := range dnsRulesOf(cfg) {
		if isProxyModeDNSRule(r) {
			t.Fatalf("left over after leaving proxy mode: %v", r)
		}
	}
}
