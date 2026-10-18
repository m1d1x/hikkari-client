package profile

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseHysteria2URI_Smoke(t *testing.T) {
	uri := "hysteria2://bitFhbuhm8Oy6VgCoPIKvrVf@92.246.139.199:443/?insecure=1&obfs=salamander&obfs-password=GbWPPp80rsBQZZvftDZMDr8S&sni=www.bing.com#NEKKO-HY2"
	d, err := ParseURI(uri)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if d.Kind != KindOutbound {
		t.Fatalf("kind = %q, want %q", d.Kind, KindOutbound)
	}
	if d.Label != "NEKKO-HY2" {
		t.Fatalf("label = %q", d.Label)
	}
	want := map[string]interface{}{
		"type":        "hysteria2",
		"server":      "92.246.139.199",
		"server_port": 443,
		"password":    "bitFhbuhm8Oy6VgCoPIKvrVf",
		"tls": map[string]interface{}{
			"enabled":     true,
			"server_name": "www.bing.com",
			"insecure":    true,
		},
		"obfs": map[string]interface{}{
			"type":     "salamander",
			"password": "GbWPPp80rsBQZZvftDZMDr8S",
		},
	}
	got, _ := json.Marshal(d.Config)
	wantJSON, _ := json.Marshal(want)
	if string(got) != string(wantJSON) {
		t.Fatalf("config mismatch:\ngot:  %s\nwant: %s", got, wantJSON)
	}
}

func TestParseVLESS_RealityWS(t *testing.T) {
	uri := "vless://aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee@example.com:443?" +
		"security=reality&sni=www.cloudflare.com&fp=chrome&pbk=KEYHERE&sid=123abc&" +
		"type=ws&path=%2Fws&host=cdn.example.com&flow=xtls-rprx-vision#My%20VLESS"
	d, err := ParseURI(uri)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if d.Kind != KindOutbound || d.Label != "My VLESS" {
		t.Fatalf("kind/label wrong: %q / %q", d.Kind, d.Label)
	}
	got, _ := json.Marshal(d.Config)
	for _, want := range []string{
		`"type":"vless"`, `"uuid":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"`,
		`"server":"example.com"`, `"server_port":443`,
		`"flow":"xtls-rprx-vision"`,
		`"server_name":"www.cloudflare.com"`,
		`"fingerprint":"chrome"`, `"reality"`, `"public_key":"KEYHERE"`,
		`"transport"`, `"type":"ws"`, `"path":"/ws"`, `"Host":"cdn.example.com"`,
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("missing %q in config:\n%s", want, got)
		}
	}
}

func TestParseTrojan(t *testing.T) {
	uri := "trojan://mypassword@example.com:443?sni=example.com#Trojan"
	d, err := ParseURI(uri)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got, _ := json.Marshal(d.Config)
	for _, want := range []string{
		`"type":"trojan"`, `"password":"mypassword"`, `"server":"example.com"`,
		`"server_port":443`, `"tls"`, `"server_name":"example.com"`,
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("missing %q in config:\n%s", want, got)
		}
	}
}

func TestParseShadowsocks_Plain(t *testing.T) {
	uri := "ss://chacha20-ietf-poly1305:secret@example.com:8388#SS"
	d, err := ParseURI(uri)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got, _ := json.Marshal(d.Config)
	for _, want := range []string{
		`"type":"shadowsocks"`, `"method":"chacha20-ietf-poly1305"`,
		`"password":"secret"`, `"server":"example.com"`, `"server_port":8388`,
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("missing %q in config:\n%s", want, got)
		}
	}
}

func TestParseShadowsocks_Base64Userinfo(t *testing.T) {
	// base64("aes-256-gcm:hunter2") = "YWVzLTI1Ni1nY206aHVudGVyMg=="
	uri := "ss://YWVzLTI1Ni1nY206aHVudGVyMg@example.com:8388#SS"
	d, err := ParseURI(uri)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if d.Config["method"] != "aes-256-gcm" || d.Config["password"] != "hunter2" {
		t.Fatalf("got method=%v password=%v", d.Config["method"], d.Config["password"])
	}
}

// A fresh install has no profiles.json — the first import must still work
// and materialise the file (regression: Add used to fail with ENOENT).
func TestAddOnFreshInstall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "profiles.json")
	s := New(path)

	if s.Exists() {
		t.Fatal("profiles.json should not exist yet")
	}
	name, err := s.Add("Germany", KindOutbound, map[string]interface{}{
		"type": "vless", "server": "144.31.104.44", "server_port": 443,
	})
	if err != nil {
		t.Fatalf("Add on empty store: %v", err)
	}
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != name {
		t.Fatalf("List = %+v, want the profile just added (%q)", list, name)
	}
}
