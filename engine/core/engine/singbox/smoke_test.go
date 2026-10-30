package singbox

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
	s := NewStore(path)

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

func TestParseVLESS_XHTTP_Zovfy(t *testing.T) {
	uri := "vless://c18cef8f-a1a7-4f58-b32b-21d54b8ad4f2@web.zovfy.ru:443?encryption=none&type=xhttp&path=%2Fartwork%2Fapi%2Fuploads%2Fsync-3cfbabbe8c07&mode=auto&security=tls&sni=web.zovfy.ru&fp=edge&alpn=h2%2Chttp%2F1.1#%F0%9F%87%AB%F0%9F%87%AE%D0%A4%D0%98%D0%9D%D0%9A%D0%90%20%D0%A1%D0%A2%D0%90%D0%A0%D0%90%D0%AF"
	d, err := ParseURI(uri)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if d.Kind != KindOutbound {
		t.Fatalf("kind = %q, want %q", d.Kind, KindOutbound)
	}
	if d.Label != "🇫🇮ФИНКА СТАРАЯ" {
		t.Fatalf("label = %q", d.Label)
	}
	tr, ok := d.Config["transport"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing transport block in config: %#v", d.Config)
	}
	if tr["type"] != "xhttp" || tr["path"] != "/artwork/api/uploads/sync-3cfbabbe8c07" || tr["mode"] != "auto" || tr["host"] != "web.zovfy.ru" {
		t.Fatalf("unexpected transport: %#v", tr)
	}
	tls, ok := d.Config["tls"].(map[string]interface{})
	if !ok || tls["server_name"] != "web.zovfy.ru" {
		t.Fatalf("unexpected tls: %#v", tls)
	}
}

func TestParseURIs_Subscription(t *testing.T) {
	sub := "dmxlc3M6Ly9jMThjZWY4Zi1hMWE3LTRmNTgtYjMyYi0yMWQ1NGI4YWQ0ZjJAYWxpbmF6YWJyaWtvc2luYS56b3ZmeS5ydTo0NDM/ZW5jcnlwdGlvbj1ub25lJnR5cGU9eGh0dHAmcGF0aD0lMkZhcnR3b3JrJTJGYXBpJTJGdXBsb2FkcyUyRnN5bmMtM2NmYmFiYmU1YzAzJm1vZGU9YXV0byZzZWN1cml0eT10bHMmc25pPWFsaW5hemFicmlrb3NpbmEuem92ZnkucnUmZnA9ZWRnZSZhbHBuPWgyJTJDaHR0cCUyRjEuMSMlRjAlOUYlODclQUIlRjAlOUYlODclQUUlMjAlRDAlQTQlRDAlOTglRDAlOUQlRDAlOUElRDAlOTAKdmxlc3M6Ly9jMThjZWY4Zi1hMWE3LTRmNTgtYjMyYi0yMWQ1NGI4YWQ0ZjJAcG9tb3lrYS5pbmMuem92ZnkucnU6NDQzP2VuY3J5cHRpb249bm9uZSZ0eXBlPXhodHRwJnBhdGg9JTJGYXJ0d29yayUyRmFwaSUyRnVwbG9hZHMmbW9kZT1hdXRvJnNlY3VyaXR5PXRscyZzbmk9cG9tb3lrYS5pbmMuem92ZnkucnUmZnA9ZWRnZSZhbHBuPWgyJTJDaHR0cCUyRjEuMSMlRjAlOUYlODclQTklRjAlOUYlODclQUElMjAlRDAlOTMlRDAlOTUlRDAlQTAlRDAlOUMlRDAlOTAlRDAlOUQlRDAlQTElRDAlOUElMjBYSFRUUAp2bGVzczovL2MxOGNlZjhmLWExYTctNGY1OC1iMzJiLTIxZDU0YjhhZDRmMkB3ZWIuem92ZnkucnU6NDQzP2VuY3J5cHRpb249bm9uZSZ0eXBlPXhodHRwJnBhdGg9JTJGYXJ0d29yayUyRmFwaSUyRnVwbG9hZHMlMkZzeW5jLTNjZmJhYmJlOGMwNyZtb2RlPWF1dG8mc2VjdXJpdHk9dGxzJnNuaT13ZWIuem92ZnkucnUmZnA9ZWRnZSZhbHBuPWgyJTJDaHR0cCUyRjEuMSMlRjAlOUYlODclQUIlRjAlOUYlODclQUUlRDAlQTQlRDAlOTglRDAlOUQlRDAlOUElRDAlOTAlMjAlRDAlQTElRDAlQTIlRDAlOTAlRDAlQTAlRDAlOTAlRDAlQUYKc3M6Ly9ZMmhoWTJoaE1qQXRhV1YwWmkxd2IyeDVNVE13TlRwWlRYSmhla1pIY1VaeVEzbzFYMGRIWXpCb05sbGlUa1I2UldjdFFYbGlkdz09QG5pa3lkYToxMjM0IyVEMCU5RSVEMSU4MSVEMSU4MiVEMCVCMCVEMCVCQiVEMCVCRSVEMSU4MSVEMSU4QyUyMCVEMCU5NCVEMCU5RC4lMjAxMCUyMCU1QlBlbG1lc2thX3MlNUQ="
	drafts, err := ParseURIs(sub)
	if err != nil {
		t.Fatalf("ParseURIs: %v", err)
	}
	if len(drafts) != 4 {
		t.Fatalf("expected 4 drafts, got %d", len(drafts))
	}
	if drafts[0].Label != "🇫🇮 ФИНКА" {
		t.Errorf("draft[0].Label = %q", drafts[0].Label)
	}
	if drafts[1].Label != "🇩🇪 ГЕРМАНСК XHTTP" {
		t.Errorf("draft[1].Label = %q", drafts[1].Label)
	}
	if drafts[2].Label != "🇫🇮ФИНКА СТАРАЯ" {
		t.Errorf("draft[2].Label = %q", drafts[2].Label)
	}
}
