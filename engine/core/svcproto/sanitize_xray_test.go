package svcproto_test

import (
	"encoding/json"
	"strings"
	"testing"

	"NekkoVPN/core/engine"
	"NekkoVPN/core/engine/xray"
	"NekkoVPN/core/routing"
	"NekkoVPN/core/svcproto"
)

// compiled is what the GUI really sends: the Xray adapter's own output.
func compiled(t *testing.T, link string) map[string]interface{} {
	t.Helper()
	ps, err := xray.Decode(link)
	if err != nil {
		t.Fatal(err)
	}
	ps[0].Name = "server"
	cfg, err := xray.CompileVPN(engine.Configuration{
		Profiles: ps, ActiveProfile: "server", AutoSelect: true, Routing: routing.DefaultRoutingState(),
	}, map[string]int{"server": 20808})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(cfg)
	var out map[string]interface{}
	_ = json.Unmarshal(raw, &out)
	return out
}

func sanitize(t *testing.T, cfg map[string]interface{}) (string, error) {
	t.Helper()
	raw, _ := json.Marshal(cfg)
	_, dns, err := svcproto.SanitizeXray(raw)
	return dns, err
}

func TestSanitizeXrayAcceptsWhatTheGUICompiles(t *testing.T) {
	for _, link := range []string{
		"vless://11111111-2222-3333-4444-555555555555@example.com:443?security=reality&pbk=key&sid=ab&type=xhttp&path=%2Fx&extra=%7B%22xPaddingBytes%22%3A%22100-1000%22%7D#R",
		"trojan://secret@example.com:443?type=ws&path=%2Fws&host=cdn.example.com#T",
		"hy2://auth@example.com:443?sni=example.com#H",
	} {
		dns, err := sanitize(t, compiled(t, link))
		if err != nil {
			t.Fatalf("%s: %v", link, err)
		}
		if dns != "172.31.7.1" {
			t.Fatalf("DNS address %q, want the TUN gateway", dns)
		}
	}
}

func TestSanitizeXrayRefusesWhatOpensFilesOrListens(t *testing.T) {
	cases := map[string]func(c map[string]interface{}){
		"log.access": func(c map[string]interface{}) {
			c["log"].(map[string]interface{})["access"] = "/Library/LaunchDaemons/x.plist"
		},
		"api section": func(c map[string]interface{}) { c["api"] = map[string]interface{}{"tag": "api"} },
		"certificateFile": func(c map[string]interface{}) {
			out := c["outbounds"].([]interface{})[1].(map[string]interface{})
			out["streamSettings"].(map[string]interface{})["tlsSettings"] = map[string]interface{}{
				"certificates": []interface{}{map[string]interface{}{"certificateFile": "/etc/master.passwd"}},
			}
		},
		"masterKeyLog": func(c map[string]interface{}) {
			out := c["outbounds"].([]interface{})[1].(map[string]interface{})
			out["streamSettings"].(map[string]interface{})["tlsSettings"] = map[string]interface{}{"masterKeyLog": "/tmp/k"}
		},
		"ext geodata": func(c map[string]interface{}) {
			rules := c["routing"].(map[string]interface{})["rules"].([]interface{})
			rules[0].(map[string]interface{})["domain"] = []interface{}{"ext:/etc/x.dat:tag"}
		},
		"socks off loopback": func(c map[string]interface{}) {
			c["inbounds"].([]interface{})[0].(map[string]interface{})["listen"] = "0.0.0.0"
		},
		"unix socket": func(c map[string]interface{}) {
			c["inbounds"].([]interface{})[0].(map[string]interface{})["listen"] = "/var/run/x.sock"
		},
		"other inbound": func(c map[string]interface{}) {
			c["inbounds"] = append(c["inbounds"].([]interface{}), map[string]interface{}{"protocol": "dokodemo-door", "listen": "127.0.0.1"})
		},
		"other outbound": func(c map[string]interface{}) {
			c["outbounds"] = append(c["outbounds"].([]interface{}), map[string]interface{}{"protocol": "loopback"})
		},
		"no TUN": func(c map[string]interface{}) {
			c["inbounds"] = c["inbounds"].([]interface{})[:1]
		},
	}
	for name, mutate := range cases {
		c := compiled(t, "trojan://secret@example.com:443#T")
		mutate(c)
		if _, err := sanitize(t, c); err == nil {
			t.Errorf("%s: accepted", name)
		} else if strings.Contains(err.Error(), "secret") {
			t.Errorf("%s: error leaks the config: %v", name, err)
		}
	}
}
