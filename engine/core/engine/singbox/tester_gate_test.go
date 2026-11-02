package singbox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"NekkoVPN/core/engine"
	"NekkoVPN/core/engine/singbox/config"
	"NekkoVPN/core/profile"
	"NekkoVPN/core/routing"
)

func TestFlowRetryReachesOnlyATestersTCPOutbounds(t *testing.T) {
	profiles := []profile.Profile{
		{Name: "tcp", Label: "TCP", Kind: profile.KindOutbound, Config: map[string]interface{}{
			"type": "vless", "server": "192.0.2.1", "server_port": float64(443), "uuid": "bf000d23-0000-4000-8000-000000000000",
		}},
		{Name: "udp", Label: "UDP", Kind: profile.KindOutbound, Config: map[string]interface{}{
			"type": "hysteria2", "server": "192.0.2.2", "server_port": float64(443), "password": "x",
		}},
	}
	configure := func(tester bool) map[string]map[string]interface{} {
		t.Helper()
		path := filepath.Join(t.TempDir(), "config.json")
		b := &Backend{config: configfile.New(path)}
		b.tester = func() bool { return tester }
		if err := b.Configure(engine.Configuration{Profiles: profiles, ActiveProfile: "tcp", Routing: routing.DefaultRoutingState()}); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var cfg struct {
			Outbounds []map[string]interface{} `json:"outbounds"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			t.Fatal(err)
		}
		out := map[string]map[string]interface{}{}
		for _, o := range cfg.Outbounds {
			tag, _ := o["tag"].(string)
			out[tag] = o
		}
		return out
	}

	on := configure(true)
	if on["p-tcp"]["flow_retry_delay"] != flowRetryDelay {
		t.Fatalf("tester's TCP outbound: %v", on["p-tcp"])
	}
	if _, set := on["p-udp"]["flow_retry_delay"]; set {
		t.Fatal("a UDP outbound has no TCP dial to retry")
	}
	off := configure(false)
	if _, set := off["p-tcp"]["flow_retry_delay"]; set {
		t.Fatal("a regular user must not get the retry yet")
	}
}
