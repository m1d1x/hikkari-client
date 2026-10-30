package singbox

import (
	"reflect"
	"testing"
)

// Round-trip: a URI parsed then exported then re-parsed must yield the same
// sing-box config. Catches asymmetries between parse.go and export.go.
func TestExportRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		uri  string
	}{
		{"hysteria2", "hysteria2://pass123@h2.example.com:443?sni=cdn.example.com&insecure=1&obfs=salamander&obfs-password=obf#My%20HY2"},
		{"vless-tls-ws", "vless://11111111-2222-3333-4444-555555555555@vl.example.com:443?packetEncoding=xudp&security=tls&sni=vl.example.com&fp=chrome&type=ws&path=/ws&host=cdn.example.com#VL"},
		{"vless-reality", "vless://11111111-2222-3333-4444-555555555555@re.example.com:443?packetEncoding=xudp&security=reality&sni=www.microsoft.com&fp=chrome&pbk=PUBKEY&sid=abcd&flow=xtls-rprx-vision#RE"},
		{"vless-xhttp", "vless://11111111-2222-3333-4444-555555555555@xh.example.com:443?host=xh.example.com&mode=auto&packetEncoding=xudp&path=/artwork/api/uploads&security=tls&sni=xh.example.com&type=xhttp#XH"},
		{"trojan", "trojan://trojanpass@tj.example.com:443?security=tls&sni=tj.example.com#TJ"},
		{"shadowsocks", "ss://YWVzLTI1Ni1nY206c2VjcmV0@ss.example.com:8388#SS"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d1, err := ParseURI(tc.uri)
			if err != nil {
				t.Fatalf("first parse: %v", err)
			}
			p := Profile{Name: "n", Label: d1.Label, Kind: d1.Kind, Config: d1.Config}
			out, err := ExportURI(p)
			if err != nil {
				t.Fatalf("export: %v", err)
			}
			d2, err := ParseURI(out)
			if err != nil {
				t.Fatalf("re-parse %q: %v", out, err)
			}
			if !reflect.DeepEqual(d1.Config, d2.Config) {
				t.Errorf("config mismatch\n in:  %#v\n out: %#v\n uri: %s", d1.Config, d2.Config, out)
			}
		})
	}
}

func TestExportWireGuardReimports(t *testing.T) {
	conf := "[Interface]\nPrivateKey = privkey123\nAddress = 10.0.0.2/32\nJc = 4\nJmin = 40\nJmax = 70\n\n[Peer]\nPublicKey = pubkey456\nAllowedIPs = 0.0.0.0/0\nEndpoint = wg.example.com:51820\nPersistentKeepalive = 25\n"
	d1, err := ParseURI(conf)
	if err != nil {
		t.Fatalf("parse conf: %v", err)
	}
	p := Profile{Name: "n", Label: d1.Label, Kind: d1.Kind, Config: d1.Config}
	out, err := ExportURI(p)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	d2, err := ParseURI(out)
	if err != nil {
		t.Fatalf("re-parse conf: %v", err)
	}
	if !reflect.DeepEqual(d1.Config, d2.Config) {
		t.Errorf("wg config mismatch\n in:  %#v\n out: %#v\n conf:\n%s", d1.Config, d2.Config, out)
	}
}
