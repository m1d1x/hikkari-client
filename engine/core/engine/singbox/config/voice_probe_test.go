package configfile

import (
	"testing"

	"NekkoVPN/core/voiceprobe"
)

// voiceProbeRule is the rule that matches the probe's inbound, and its index.
func voiceProbeRule(cfg map[string]interface{}) (map[string]interface{}, int) {
	for i, r := range rulesOf(cfg) {
		in, _ := r["inbound"].([]interface{})
		if len(in) == 1 && in[0] == voiceprobe.Tag && r["network"] == nil {
			return r, i
		}
	}
	return nil, -1
}

func TestTheCoreCarriesTheVoiceProbesInboundOnLoopback(t *testing.T) {
	for name, inbounds := range map[string]interface{}{
		"a fresh config":  DefaultConfig()["inbounds"],
		"a healed config": coreInbounds(),
	} {
		var probe map[string]interface{}
		for _, in := range inbounds.([]interface{}) {
			if m := in.(map[string]interface{}); m["tag"] == voiceprobe.Tag {
				probe = m
			}
		}
		if probe == nil {
			t.Fatalf("%s: no inbound %q among %v", name, voiceprobe.Tag, inbounds)
		}
		if probe["type"] != "direct" || probe["listen"] != "127.0.0.1" || probe["network"] != "udp" {
			t.Fatalf("%s: the probe's inbound is not a UDP forwarder on loopback: %v", name, probe)
		}
		if probe["override_address"] != "stun.cloudflare.com" || probe["override_port"] != 3478 {
			t.Fatalf("%s: the probe's inbound forwards elsewhere: %v", name, probe)
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
		rule, at := voiceProbeRule(cfg)
		if rule == nil {
			t.Fatalf("%s: no rule for the probe's inbound in %v", name, rulesOf(cfg))
		}
		if rule["outbound"] != SelectorTag {
			t.Fatalf("%s: the probe goes to %v, not the VPN", name, rule["outbound"])
		}
		for i, r := range rulesOf(cfg)[:at] {
			if ob, _ := r["outbound"].(string); ob != "" && r["network"] != "icmp" {
				t.Fatalf("%s: rule %d sends traffic to %q ahead of the probe's: %v", name, i, ob, r)
			}
		}
	}
}

// In auto mode a call's datagrams take auto mode's UDP group, so the probe's
// datagrams go there too.
func TestTheVoiceProbeTakesAutoModesUDPGroup(t *testing.T) {
	cfg := freshCfg()
	cfg["outbounds"] = []interface{}{
		map[string]interface{}{"tag": AutoTag, "type": "urltest"},
		map[string]interface{}{"tag": AutoUDPTag, "type": "urltest"},
	}
	ApplyRouting(cfg, RoutingState{Enabled: true, Mode: ModeProxy, Proxy: RouteList{Domains: []string{"discord.com"}}}, AutoTag)
	_, at := voiceProbeRule(cfg)
	if at < 1 {
		t.Fatalf("no rule for the probe's inbound behind its UDP twin: %v", rulesOf(cfg))
	}
	twin := rulesOf(cfg)[at-1]
	in, _ := twin["inbound"].([]interface{})
	if len(in) != 1 || in[0] != voiceprobe.Tag || twin["network"] != "udp" || twin["outbound"] != AutoUDPTag {
		t.Fatalf("the probe's UDP does not go to %s: %v", AutoUDPTag, twin)
	}
}
