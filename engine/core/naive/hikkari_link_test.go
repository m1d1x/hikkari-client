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

// v1.07: entries= names the cascade's further entries; the outbound gets
// them as entries, and a profile goes back to the same link.
func TestHikkariEntriesLink(t *testing.T) {
	raw := "hikkari+v1://hikkari:subscriber-key-0123456789@179.253.75.148:443?dup=1&sni=vm.example&entries=203.0.113.9%3A8443%2C%5B2001%3Adb8%3A%3A1%5D%3A443#Hikkari"
	u, _ := url.Parse(raw)
	l, err := Parse(u)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entries) != 2 || l.Entries[0] != "203.0.113.9:8443" || l.Entries[1] != "[2001:db8::1]:443" {
		t.Fatalf("entries %q", l.Entries)
	}
	o := l.Outbound()
	entries, _ := o["entries"].([]interface{})
	first, _ := entries[0].(map[string]interface{})
	second, _ := entries[1].(map[string]interface{})
	if len(entries) != 2 || first["server"] != "203.0.113.9" || first["server_port"] != 8443 || second["server"] != "2001:db8::1" {
		t.Fatalf("outbound entries %#v", o["entries"])
	}
	// A profile read back from JSON has float ports.
	o["entries"] = []interface{}{
		map[string]interface{}{"server": "203.0.113.9", "server_port": float64(8443)},
		map[string]interface{}{"server": "2001:db8::1", "server_port": float64(443)},
	}
	back, err := FromOutbound(o, l.Label)
	if err != nil || len(back.Entries) != 2 || back.Entries[1] != "[2001:db8::1]:443" {
		t.Fatalf("back %+v %v", back, err)
	}
	u2, _ := url.Parse(back.URI())
	if got := u2.Query().Get("entries"); got != "203.0.113.9:8443,[2001:db8::1]:443" {
		t.Fatalf("uri %s entries %q", back.URI(), got)
	}
	if again, err := Parse(u2); err != nil || len(again.Entries) != 2 {
		t.Fatalf("uri does not parse back: %v", err)
	}
	// Without entries nothing changes.
	plain, _ := url.Parse("hikkari+v1://hikkari:subscriber-key-0123456789@179.253.75.148:443?sni=vm.example")
	if l, _ := Parse(plain); l.Entries != nil || l.Outbound()["entries"] != nil {
		t.Fatal("a link without entries got some")
	}
	for _, bad := range []string{
		"hikkari+v1://hikkari:k@179.253.75.148:443?entries=203.0.113.9",
		"hikkari+v1://hikkari:k@179.253.75.148:443?entries=203.0.113.9%3A0",
		"hikkari+v1://hikkari:k@179.253.75.148:443?entries=203.0.113.9%3A443%2C",
		"naive+https://u:k@179.253.75.148:443?entries=203.0.113.9%3A443",
	} {
		u, _ := url.Parse(bad)
		if _, err := Parse(u); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
	naive := map[string]interface{}{"type": "naive", "server": "h", "server_port": 443, "username": "u", "password": "p",
		"tls": map[string]interface{}{"enabled": true}, "entries": o["entries"]}
	if _, err := FromOutbound(naive, ""); err == nil {
		t.Fatal("a naive profile kept entries")
	}
}
