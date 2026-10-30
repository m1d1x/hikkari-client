package singbox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"NekkoVPN/core/engine"
	configfile "NekkoVPN/core/engine/singbox/config"
	"NekkoVPN/core/profile"
	"NekkoVPN/core/routing"
)

func TestPortableProfilesCompileWithoutChangingLegacyConfig(t *testing.T) {
	dir := t.TempDir()
	b := &Backend{config: configfile.New(filepath.Join(dir, "config.json"))}
	old := profile.Profile{Name: "legacy", Label: "Legacy", Kind: profile.KindOutbound, Config: map[string]interface{}{"type": "vless", "server": "127.0.0.1", "server_port": 443, "uuid": "id", "custom_legacy_option": "preserve"}}
	normalized, err := b.Normalize(old)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Connection == nil || normalized.Config["custom_legacy_option"] != "preserve" {
		t.Fatal("legacy migration lost native options")
	}
	fresh, err := b.Decode("vless://id@127.0.0.1:8443?security=tls&sni=example.com#New")
	if err != nil {
		t.Fatal(err)
	}
	fresh[0].Name = "portable"
	if fresh[0].Config != nil || fresh[0].Kind != "" {
		t.Fatal("new profile is engine-native")
	}
	before, _ := json.Marshal(old.Config)
	if err := b.Configure(engine.Configuration{Profiles: []profile.Profile{normalized, fresh[0]}, ActiveProfile: "portable", Routing: routing.DefaultRoutingState()}); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(old.Config)
	if string(before) != string(after) {
		t.Fatal("compiler mutated a stored legacy profile")
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, v := range cfg["outbounds"].([]interface{}) {
		if v.(map[string]interface{})["type"] == "vless" {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("compiled %d servers, want legacy and portable", count)
	}
}
