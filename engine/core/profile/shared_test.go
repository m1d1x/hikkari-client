package profile

import "testing"

func TestProtoSlugFromConnection(t *testing.T) {
	cases := map[string]string{
		"vless://u@h:443?security=reality&type=xhttp#a": "vless-reality-xhttp",
		"vless://u@h:443?security=tls&type=tcp#a":       "vless",
		"hysteria2://p@h:443#a":                         "hysteria2",
	}
	for uri, want := range cases {
		proto := uri[:len(uri)-len(uri[len("vless"):])]
		if uri[0] == 'h' {
			proto = "hysteria2"
		}
		got := ProtoSlug(Profile{Connection: &Connection{URI: uri, Protocol: proto}})
		if got != want {
			t.Errorf("%s: got %q, want %q", uri, got, want)
		}
	}
}
