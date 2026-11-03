package profile

import (
	"encoding/json"
	"testing"
)

func TestSharedFollowsSubscription(t *testing.T) {
	sub := Profile{Name: "sub", Subscription: &SubscriptionInfo{ID: "s1"}}
	own := Profile{Name: "own", Connection: &Connection{URI: "vless://x@example.com:443", Protocol: "vless"}}
	if !sub.Shared() || own.Shared() {
		t.Fatalf("Shared(): subscription=%v own=%v, want true/false", sub.Shared(), own.Shared())
	}
	var legacy Profile
	if err := json.Unmarshal([]byte(`{"name":"old","config":{"type":"vless"}}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.Shared() {
		t.Error("legacy profile without subscription must stay private")
	}
}

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
