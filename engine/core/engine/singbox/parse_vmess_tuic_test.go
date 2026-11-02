package singbox

import (
	"encoding/base64"
	"testing"

	"NekkoVPN/core/profile"
)

func TestVMessLinkSurvivesExport(t *testing.T) {
	raw := `{"v":"2","ps":"Берлин","add":"a.example","port":"443","id":"uuid-1","aid":"0","scy":"auto","net":"ws","host":"h.example","path":"/p","tls":"tls","sni":"s.example"}`
	first, err := ParseURI("vmess://" + base64.StdEncoding.EncodeToString([]byte(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if first.Label != "Берлин" || first.Config["type"] != "vmess" || first.Config["server_port"] != 443 {
		t.Fatalf("draft = %#v", first)
	}
	tr, _ := first.Config["transport"].(map[string]interface{})
	if tr["type"] != "ws" || tr["path"] != "/p" {
		t.Fatalf("transport = %#v", tr)
	}
	tls, _ := first.Config["tls"].(map[string]interface{})
	if tls["server_name"] != "s.example" {
		t.Fatalf("tls = %#v", tls)
	}

	uri, err := ExportURI(profile.Profile{Label: first.Label, Kind: first.Kind, Config: first.Config})
	if err != nil {
		t.Fatal(err)
	}
	second, err := ParseURI(uri)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := second.Config["transport"].(map[string]interface{})
	againTLS, _ := second.Config["tls"].(map[string]interface{})
	if second.Label != "Берлин" || again["path"] != "/p" || againTLS["server_name"] != "s.example" || second.Config["uuid"] != "uuid-1" {
		t.Fatalf("round trip = %#v", second)
	}
}

func TestTUICLinkSurvivesExport(t *testing.T) {
	first, err := ParseURI("tuic://uuid-1:secret@a.example:443?sni=s.example&alpn=h3&congestion_control=bbr&udp_relay_mode=native#TUIC")
	if err != nil {
		t.Fatal(err)
	}
	uri, err := ExportURI(profile.Profile{Label: first.Label, Kind: first.Kind, Config: first.Config})
	if err != nil {
		t.Fatal(err)
	}
	second, err := ParseURI(uri)
	if err != nil {
		t.Fatal(err)
	}
	tls, _ := second.Config["tls"].(map[string]interface{})
	if second.Config["type"] != "tuic" || second.Config["password"] != "secret" || second.Config["congestion_control"] != "bbr" ||
		second.Config["udp_relay_mode"] != "native" || tls["server_name"] != "s.example" || second.Label != "TUIC" {
		t.Fatalf("round trip = %#v", second)
	}
}
