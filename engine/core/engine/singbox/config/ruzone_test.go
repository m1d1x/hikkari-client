package configfile

import (
	"slices"
	"testing"

	"NekkoVPN/core/ruzone"
)

// isRUZoneRule recognises the route rule ruZoneRules compiles.
func isRUZoneRule(m map[string]interface{}) bool {
	suf, _ := m["domain_suffix"].([]interface{})
	return len(suf) == len(ruzone.Domains()) && suf[0] == ruzone.Domains()[0] && m["ip_cidr"] != nil
}

func ruZoneIndex(rules []interface{}) int {
	return ruleIndex(rules, isRUZoneRule)
}

// Every mode and the master switch: the Russian internet goes direct, once.
func TestTheRussianZoneGoesDirectInEveryMode(t *testing.T) {
	for _, state := range []RoutingState{
		{Enabled: false, Mode: ModeBypass},
		{Enabled: true, Mode: ModeBypass, Bypass: RouteList{Processes: []string{"steam.exe"}}},
		{Enabled: true, Mode: ModeProxy, Proxy: RouteList{Domains: []string{"x.com"}}},
	} {
		cfg := DefaultConfig()
		for i := 0; i < 2; i++ { // second pass checks idempotency
			ApplyRouting(cfg, state, SelectorTag)
		}
		rules := cfg["route"].(map[string]interface{})["rules"].([]interface{})
		n := 0
		for _, r := range rules {
			m := r.(map[string]interface{})
			if !isRUZoneRule(m) {
				continue
			}
			n++
			if m["outbound"] != "direct" {
				t.Fatalf("%+v: Russian zone goes to %v", state, m["outbound"])
			}
			if !slices.Contains(m["ip_cidr"].([]interface{}), interface{}("2a02:6b8::/29")) {
				t.Fatalf("%+v: Russian zone without the edge's addresses", state)
			}
		}
		if n != 1 {
			t.Fatalf("%+v: %d Russian zone rules, want 1", state, n)
		}
		dns := 0
		for _, r := range cfg["dns"].(map[string]interface{})["rules"].([]interface{}) {
			if isRUZoneDNSRule(r.(map[string]interface{})) {
				dns++
			}
		}
		if dns != 1 {
			t.Fatalf("%+v: %d Russian zone dns rules, want 1", state, dns)
		}
	}
}

// With the tunnel as the default the zone must see the network as without the
// VPN: ahead of the QUIC reject.
func TestTheRussianZoneComesBeforeTheQUICReject(t *testing.T) {
	cfg := DefaultConfig()
	ensureICMPDirectRule(cfg, true)
	ApplyRouting(cfg, RoutingState{Enabled: false, Mode: ModeBypass}, SelectorTag)
	rules := cfg["route"].(map[string]interface{})["rules"].([]interface{})
	zone, quic := ruZoneIndex(rules), ruleIndex(rules, isQUICReject)
	if zone < 0 || quic < 0 || zone > quic {
		t.Fatalf("Russian zone at %d, QUIC reject at %d", zone, quic)
	}
}

// A Russian site the user sent through the VPN on purpose stays there: the
// zone comes after the user's rules, and before the escapes from direct.
func TestAUserRuleBeatsTheRussianZone(t *testing.T) {
	cfg := DefaultConfig()
	ApplyRouting(cfg, RoutingState{Enabled: true, Mode: ModeProxy, Proxy: RouteList{Domains: []string{"ozon.ru"}}}, SelectorTag)
	rules := cfg["route"].(map[string]interface{})["rules"].([]interface{})
	user := ruleIndex(rules, func(m map[string]interface{}) bool {
		suf, _ := m["domain_suffix"].([]interface{})
		return len(suf) == 1 && suf[0] == "ozon.ru" && m["outbound"] == SelectorTag
	})
	escape := ruleIndex(rules, func(m map[string]interface{}) bool {
		_, ports := m["port"].([]interface{})
		return ports && m["outbound"] == SelectorTag
	})
	zone := ruZoneIndex(rules)
	if user < 0 || escape < 0 || !(user < zone && zone < escape) {
		t.Fatalf("user rule at %d, Russian zone at %d, escapes at %d", user, zone, escape)
	}
}
