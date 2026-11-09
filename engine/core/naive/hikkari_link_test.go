package naive

import (
	"net/url"
	"testing"
)

func TestHikkariLinkRoundTrip(t *testing.T) {
	raw := "hikkari+v1://hikkari:subscriber-key-0123456789@179.253.75.148:443?sni=vm.example&hikkari-node=n%3Anvn1#Hikkari%20nvn1"
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	l, err := Parse(u)
	if err != nil {
		t.Fatal(err)
	}
	if !l.Hikkari || l.QUIC || l.Host != "179.253.75.148" || l.Port != 443 || l.ServerName != "vm.example" || l.Password != "subscriber-key-0123456789" || l.Label != "Hikkari nvn1" {
		t.Fatalf("parsed %+v", l)
	}
	o := l.Outbound()
	tls, _ := o["tls"].(map[string]interface{})
	if o["type"] != "hikkari" || o["quic"] != nil || o["username"] != "hikkari" || tls["server_name"] != "vm.example" {
		t.Fatalf("outbound %#v", o)
	}
	back, err := FromOutbound(o, l.Label)
	if err != nil || !back.Hikkari {
		t.Fatalf("back %+v %v", back, err)
	}
	u2, _ := url.Parse(back.URI())
	if u2.Scheme != "hikkari+v1" || u2.Query().Get("sni") != "vm.example" {
		t.Fatalf("uri %s", back.URI())
	}
}

func TestHikkariDupLink(t *testing.T) {
	raw := "hikkari+v1://hikkari:subscriber-key-0123456789@179.253.75.148:443?dup=1&sni=vm.example#Hikkari%20unl1"
	u, _ := url.Parse(raw)
	l, err := Parse(u)
	if err != nil || !l.Dup {
		t.Fatalf("parsed %+v %v", l, err)
	}
	o := l.Outbound()
	udp, _ := o["udp"].(map[string]interface{})
	if udp["dup"] != true {
		t.Fatalf("outbound %#v", o)
	}
	back, err := FromOutbound(o, l.Label)
	if err != nil || !back.Dup {
		t.Fatalf("back %+v %v", back, err)
	}
	if u2, _ := url.Parse(back.URI()); u2.Query().Get("dup") != "1" || u2.Query().Get("sni") != "vm.example" {
		t.Fatalf("uri %s", back.URI())
	}
	// Without dup the outbound stays v1.01.
	plain, _ := url.Parse("hikkari+v1://hikkari:subscriber-key-0123456789@179.253.75.148:443?sni=vm.example")
	if l, _ := Parse(plain); l.Dup || l.Outbound()["udp"] != nil {
		t.Fatal("v1.01 link got dup")
	}
	for _, bad := range []string{
		"hikkari+v1://hikkari:subscriber-key-0123456789@179.253.75.148:443?dup=2",
		"naive+https://u:subscriber-key-0123456789@179.253.75.148:443?dup=1",
	} {
		u, _ := url.Parse(bad)
		if _, err := Parse(u); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}
