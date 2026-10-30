package engine_test

import (
	"os"
	"reflect"
	"testing"

	"NekkoVPN/core/engine"
	config "NekkoVPN/core/engine/singbox/config"
	"NekkoVPN/core/engine/xray"
	"NekkoVPN/core/routing"
)

// One portable fixture exercises both native compilers, every platform, both
// modes and the master switch. Tests inspect actual emitted matcher/target pairs.
func TestAdaptersImplementTheSameRoutingIntent(t *testing.T) {
	raw, err := os.ReadFile("../routing/testdata/document-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	document, err := routing.ParseDocument(raw)
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := xray.Decode("vless://11111111-2222-3333-4444-555555555555@127.0.0.1:443#Contract")
	if err != nil {
		t.Fatal(err)
	}
	profiles[0].Name = "server"
	for _, platform := range []string{"darwin", "windows", "linux"} {
		for _, mode := range []routing.RoutingMode{routing.ModeBypass, routing.ModeProxy} {
			for _, enabled := range []bool{false, true} {
				document.Mode, document.Enabled = mode, enabled
				state, err := document.Resolve(platform)
				if err != nil {
					t.Fatal(err)
				}
				policy := state.Policy()
				sing := map[string]interface{}{}
				config.ApplyRouting(sing, state, "vpn")
				route := sing["route"].(map[string]interface{})
				if route["final"] != string(policy.DefaultTarget) {
					t.Fatalf("sing-box final: %v", route)
				}
				matches := map[string][]string{}
				for _, item := range route["rules"].([]interface{}) {
					rule := item.(map[string]interface{})
					if rule["outbound"] != string(policy.Target) {
						t.Fatal(rule)
					}
					for key, value := range rule {
						if key == "outbound" {
							continue
						}
						for _, entry := range value.([]interface{}) {
							matches[key] = append(matches[key], entry.(string))
						}
					}
				}
				for key, want := range map[string][]string{"domain_suffix": policy.Rules.Domains, "ip_cidr": policy.Rules.IPs, "process_name": policy.Rules.Processes} {
					if len(want) > 0 && !reflect.DeepEqual(matches[key], want) || len(want) == 0 && len(matches[key]) != 0 {
						t.Fatalf("sing-box %s: %v != %v", key, matches[key], want)
					}
				}
				native, err := xray.CompileVPN(engine.Configuration{Profiles: profiles, ActiveProfile: "server", Routing: state}, map[string]int{"server": 19001})
				if err != nil {
					t.Fatal(err)
				}
				target := func(t routing.Target) string {
					if t == routing.TargetDirect {
						return "direct"
					}
					return "transport-0"
				}
				rules := native["routing"].(map[string]interface{})["rules"].([]interface{})
				if rules[len(rules)-1].(map[string]interface{})["outboundTag"] != target(policy.DefaultTarget) {
					t.Fatal("Xray default target differs")
				}
				matches = map[string][]string{}
				for _, item := range rules {
					rule := item.(map[string]interface{})
					for _, key := range []string{"domain", "ip", "process"} {
						values, ok := rule[key].([]string)
						if !ok {
							continue
						}
						if rule["outboundTag"] != target(policy.Target) {
							t.Fatal(rule)
						}
						matches[key] = values
					}
				}
				domains := []string{}
				for _, domain := range policy.Rules.Domains {
					domains = append(domains, "domain:"+domain)
				}
				for key, want := range map[string][]string{"domain": domains, "ip": policy.Rules.IPs, "process": policy.Rules.Processes} {
					if len(want) > 0 && !reflect.DeepEqual(matches[key], want) || len(want) == 0 && len(matches[key]) != 0 {
						t.Fatalf("Xray %s: %v != %v", key, matches[key], want)
					}
				}
			}
		}
	}
}
