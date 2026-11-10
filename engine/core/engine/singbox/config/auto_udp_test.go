package configfile

import (
	"path/filepath"
	"reflect"
	"testing"
)

func outboundByTag(cfg map[string]interface{}, tag string) map[string]interface{} {
	for _, raw := range cfg["outbounds"].([]interface{}) {
		if out := raw.(map[string]interface{}); out["tag"] == tag {
			return out
		}
	}
	return nil
}

func rulesTo(cfg map[string]interface{}, tag string) []map[string]interface{} {
	route := cfg["route"].(map[string]interface{})
	var out []map[string]interface{}
	for _, raw := range route["rules"].([]interface{}) {
		if r, ok := raw.(map[string]interface{}); ok && r["outbound"] == tag {
			out = append(out, r)
		}
	}
	return out
}

func mixedSpecs() []ProfileSpec {
	return []ProfileSpec{
		{Tag: "p-xhttp", Kind: kindOutbound, Config: map[string]interface{}{"type": "vless", "tag": "p-xhttp"}},
		{Tag: "p-hy", Kind: kindOutbound, Config: map[string]interface{}{"type": "hysteria2", "tag": "p-hy"}},
		{Tag: "p-hk", Kind: kindOutbound, Config: map[string]interface{}{"type": "hikkari", "tag": "p-hk", "udp": map[string]interface{}{"dup": true}}},
		{Tag: "p-hk-tcp", Kind: kindOutbound, Config: map[string]interface{}{"type": "hikkari", "tag": "p-hk-tcp", "udp": map[string]interface{}{"disabled": true}}},
		{Tag: "p-naive", Kind: kindOutbound, Config: map[string]interface{}{"type": "naive", "tag": "p-naive"}},
		{Tag: "p-awg", Kind: kindEndpoint, Config: map[string]interface{}{"type": "awg", "tag": "p-awg"}},
		{Tag: "p-foreign-hy", Kind: kindOutbound, Config: map[string]interface{}{"type": "hysteria2", "tag": "p-foreign-hy"}, ExcludeFromAuto: true},
	}
}

// Auto mode's UDP goes to a group of the profiles with datagrams of their own,
// chosen by a STUN probe; TCP stays on the one auto group.
func TestAutoUDPGroupHoldsOnlyDatagramProfiles(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "config.json"))
	if err := store.RebuildActive(mixedSpecs(), "p-xhttp", true, nil); err != nil {
		t.Fatal(err)
	}
	cfg, err := store.Read()
	if err != nil {
		t.Fatal(err)
	}
	group := outboundByTag(cfg, AutoUDPTag)
	if group == nil {
		t.Fatal("no UDP group")
	}
	if !reflect.DeepEqual(group["outbounds"], []interface{}{"p-hy", "p-hk", "p-awg"}) {
		t.Fatalf("UDP group members %v", group["outbounds"])
	}
	if group["url"] != autoUDPProbe || group["mode"] != "failover" || group["type"] != "urltest" {
		t.Fatalf("UDP group %v", group)
	}
	if len(outboundByTag(cfg, AutoTag)["outbounds"].([]interface{})) != 6 {
		t.Fatal("the TCP group lost members")
	}
	route := cfg["route"].(map[string]interface{})
	if route["final"] != AutoTag {
		t.Fatalf("final %v", route["final"])
	}
	rules := route["rules"].([]interface{})
	last := rules[len(rules)-1].(map[string]interface{})
	if last["network"] != "udp" || last["outbound"] != AutoUDPTag {
		t.Fatalf("last rule %v", last)
	}
	// Rules that send traffic direct keep it ahead of the UDP rule.
	ruIndex, udpIndex := -1, -1
	for i, raw := range rules {
		r, _ := raw.(map[string]interface{})
		if r["outbound"] == "direct" && r["domain_suffix"] != nil && ruIndex < 0 {
			ruIndex = i
		}
		if r["outbound"] == AutoUDPTag && udpIndex < 0 {
			udpIndex = i
		}
	}
	if ruIndex < 0 || udpIndex < ruIndex {
		t.Fatalf("direct rule at %d, first UDP rule at %d", ruIndex, udpIndex)
	}

	// A rebuild replaces the twins, it does not add more.
	before := len(rulesTo(cfg, AutoUDPTag))
	if err := store.RebuildActive(mixedSpecs(), "p-xhttp", true, nil); err != nil {
		t.Fatal(err)
	}
	cfg, _ = store.Read()
	if after := len(rulesTo(cfg, AutoUDPTag)); after != before {
		t.Fatalf("UDP rules %d after a rebuild, %d before", after, before)
	}
}

// Every rule that ends at the auto group gets a UDP twin ahead of it.
func TestAutoUDPTwinsRulesToTheAutoGroup(t *testing.T) {
	cfg := map[string]interface{}{
		"outbounds": []interface{}{map[string]interface{}{"tag": AutoTag}, map[string]interface{}{"tag": AutoUDPTag}},
	}
	route := map[string]interface{}{
		"final": "direct",
		"rules": []interface{}{
			map[string]interface{}{"action": "sniff"},
			map[string]interface{}{"domain_suffix": []interface{}{"discord.com"}, "outbound": AutoTag},
			map[string]interface{}{"network": "tcp", "port": 443, "outbound": AutoTag},
			map[string]interface{}{"network": "udp", "port": 3478, "outbound": AutoTag},
			map[string]interface{}{"ip_cidr": []interface{}{"10.0.0.0/8"}, "outbound": "direct"},
		},
	}
	routeUDPByDatagrams(cfg, route, AutoTag)
	rules := route["rules"].([]interface{})
	if len(rules) != 6 {
		t.Fatalf("rules %v", rules)
	}
	twin := rules[1].(map[string]interface{})
	if twin["network"] != "udp" || twin["outbound"] != AutoUDPTag || twin["domain_suffix"] == nil {
		t.Fatalf("twin %v", twin)
	}
	if rules[2].(map[string]interface{})["outbound"] != AutoTag || rules[2].(map[string]interface{})["network"] != nil {
		t.Fatalf("original %v", rules[2])
	}
	if rules[3].(map[string]interface{})["outbound"] != AutoTag {
		t.Fatal("a TCP rule moved to the UDP group")
	}
	if rules[4].(map[string]interface{})["outbound"] != AutoUDPTag {
		t.Fatal("a UDP rule stayed on the auto group")
	}
	// final is direct: no trailing UDP rule.
	if last := rules[5].(map[string]interface{}); last["outbound"] != "direct" {
		t.Fatalf("last %v", last)
	}
}

// Without a profile that carries datagrams, UDP stays on the auto group as
// before; with auto off there is no UDP group at all.
func TestAutoUDPGroupAbsentWithoutDatagramProfiles(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "config.json"))
	specs := []ProfileSpec{
		{Tag: "p-xhttp", Kind: kindOutbound, Config: map[string]interface{}{"type": "vless", "tag": "p-xhttp"}},
		{Tag: "p-naive", Kind: kindOutbound, Config: map[string]interface{}{"type": "naive", "tag": "p-naive"}},
	}
	if err := store.RebuildActive(specs, "p-xhttp", true, nil); err != nil {
		t.Fatal(err)
	}
	cfg, _ := store.Read()
	if outboundByTag(cfg, AutoUDPTag) != nil || len(rulesTo(cfg, AutoUDPTag)) != 0 {
		t.Fatal("UDP group without datagram profiles")
	}
	if err := store.RebuildActive(mixedSpecs(), "p-hy", false, nil); err != nil {
		t.Fatal(err)
	}
	cfg, _ = store.Read()
	if outboundByTag(cfg, AutoUDPTag) != nil || len(rulesTo(cfg, AutoUDPTag)) != 0 {
		t.Fatal("UDP group with auto off")
	}
}
