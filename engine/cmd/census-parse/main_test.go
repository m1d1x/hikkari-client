package main

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	testUUID = "11111111-2222-4333-8444-555555555555"
	testPBK  = "SecretPublicKeyValue0123456789abcdefghijklm"
	testPass = "hunter2-secret"
)

func TestRunListsEntriesWithoutSecrets(t *testing.T) {
	body := "vless://" + testUUID + "@203.0.113.7:443?security=reality&sni=example.com&pbk=" + testPBK + "&sid=ab12&fp=chrome&type=tcp#one\n" +
		"hysteria2://" + testPass + "@203.0.113.8:8443?sni=h.example.com#two\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	var out bytes.Buffer
	if err := run(context.Background(), strings.NewReader(srv.URL+"/sub/token\n"), &out, false); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{testUUID, testPBK, testPass, "ab12", "/sub/token"} {
		if strings.Contains(out.String(), secret) {
			t.Fatalf("output leaks %q: %s", secret, out.String())
		}
	}
	var entries []Entry
	if err := json.Unmarshal(out.Bytes(), &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %+v", entries)
	}
	got := entries[0]
	want := Entry{Label: "one", Host: "203.0.113.7", Port: 443, Protocol: "vless", Transport: "tcp", Security: "reality", SNI: "example.com"}
	if got.Label != want.Label || got.Host != want.Host || got.Port != want.Port || got.Security != want.Security || got.SNI != want.SNI || got.Config != nil {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if entries[1].Protocol != "hysteria2" || entries[1].Port != 8443 {
		t.Fatalf("second = %+v", entries[1])
	}
}

func TestRunErrorHidesURL(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	err := run(context.Background(), strings.NewReader(srv.URL+"/sub/secret-token\n"), &bytes.Buffer{}, false)
	if err == nil {
		t.Fatal("want an error")
	}
	if strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("error leaks the token: %v", err)
	}
}

func TestRunAWGEndpointWithTunnelDNS(t *testing.T) {
	key := "pBNbQ3vIFMTZCbCkQtLObNUtsM9hAi+ZhlrgZbH0Xgc="
	conf := "[Interface]\nPrivateKey = " + key + "\nAddress = 10.66.0.7/32\nJc = 4\nJmin = 40\nJmax = 70\nS1 = 33\nS2 = 57\nH1 = 11\nH2 = 12\nH3 = 13\nH4 = 14\n[Peer]\nPublicKey = " + key + "\nEndpoint = 203.0.113.9:31000\nAllowedIPs = 0.0.0.0/0\n"
	doc, _ := json.Marshal(map[string]any{"containers": []any{map[string]any{"container": "amnezia-awg", "awg": map[string]any{"last_config": conf}}}})
	var encoded bytes.Buffer
	binary.Write(&encoded, binary.BigEndian, uint32(len(doc)))
	zw := zlib.NewWriter(&encoded)
	zw.Write(doc)
	zw.Close()
	link := "vpn://" + base64.RawURLEncoding.EncodeToString(encoded.Bytes()) + "?hikkari-node=n%3Aawg-one#AWG"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(link)) }))
	defer server.Close()
	for _, withConfig := range []bool{false, true} {
		var out bytes.Buffer
		if err := run(context.Background(), strings.NewReader(server.URL+"\n"), &out, withConfig); err != nil {
			t.Fatal(err)
		}
		var entries []Entry
		if err := json.Unmarshal(out.Bytes(), &entries); err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 {
			t.Fatalf("AWG was dropped: %s", out.String())
		}
		e := entries[0]
		if e.Host != "203.0.113.9" || e.Port != 31000 || e.Transport != "udp" {
			t.Fatalf("wrong endpoint: %+v", e)
		}
		if withConfig {
			if e.Config["type"] != "wireguard" || len(e.CapacityKey) != 64 || e.Node != "awg-one" || e.Resolver["detour"] != "probe" {
				t.Fatalf("incomplete measurement contract: %+v", e)
			}
			if _, ok := e.Config["dns"]; ok {
				t.Fatal("profile DNS leaked into endpoint")
			}
		} else if strings.Contains(out.String(), key) || e.Config != nil || e.Resolver != nil {
			t.Fatal("census exposed credentials")
		}
	}
}
