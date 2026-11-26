package configfile

import (
	"testing"

	"NekkoVPN/core/voiceprobe"
)

// isVoiceProbeRule reports whether r matches the voice probe's own address.
func isVoiceProbeRule(r map[string]interface{}) bool {
	cidr, _ := r["ip_cidr"].([]interface{})
	return len(cidr) == 1 && cidr[0] == voiceprobe.CIDR()
}

// voiceProbeRule finds the rule for the probe's address, and its index.
func voiceProbeRule(cfg map[string]interface{}) (map[string]interface{}, int) {
	for i, r := range rulesOf(cfg) {
		if isVoiceProbeRule(r) && r["network"] == nil {
			return r, i
		}
	}
	return nil, -1
}

// 1.0.90 had the core listen for the probe on 127.0.0.1:47391; on a PC with
// Hyper-V or WSL2 that port was reserved, and a core that cannot bind an
// inbound does not start. The core runs the TUN alone.
func TestTheCoreRunsNoInboundButTheTUN(t *testing.T) {
	for name, inbounds := range map[string]interface{}{
		"a fresh config":  DefaultConfig()["inbounds"],
		"a healed config": coreInbounds(),
	} {
		list := inbounds.([]interface{})
		if len(list) != 1 || list[0].(map[string]interface{})["type"] != "tun" {
			t.Fatalf("%s: inbounds %v, want the TUN alone", name, list)
		}
	}
}

// In the default mode only the listed sites go through the VPN and the rest
// goes direct; the probe's STUN server is not listed, and still its series
// must take the VPN's path, or the line says path=tun and times the tester's
// own network (17.11, rtt 2 ms "through the tunnel").
func TestTheVoiceProbeTakesTheVPNsPathInEveryMode(t *testing.T) {
	for name, state := range map[string]RoutingState{
		"only the listed through the VPN": {Enabled: true, Mode: ModeProxy, Proxy: RouteList{Domains: []string{"discord.com"}}},
		"all but the listed":              {Enabled: true, Mode: ModeBypass, Bypass: RouteList{Domains: []string{"ozon.ru"}}},
		"routing off":                     {Enabled: false},
	} {
		cfg := freshCfg()
		ApplyRouting(cfg, state, SelectorTag)
		route, at := voiceProbeRule(cfg)
		if route == nil {
			t.Fatalf("%s: no rule for the probe's address in %v", name, rulesOf(cfg))
		}
		if route["override_address"] != voiceprobe.TargetHost || route["override_port"] != voiceprobe.TargetPort || route["port"] != voiceprobe.Port {
			t.Fatalf("%s: the probe's datagrams are turned elsewhere: %v", name, route)
		}
		if route["outbound"] != SelectorTag {
			t.Fatalf("%s: the probe goes to %v, not the VPN", name, route["outbound"])
		}
		for i, r := range rulesOf(cfg)[:at] {
			if ob, _ := r["outbound"].(string); ob != "" && r["network"] != "icmp" {
				t.Fatalf("%s: rule %d sends traffic to %q ahead of the probe's: %v", name, i, ob, r)
			}
		}
	}
}
