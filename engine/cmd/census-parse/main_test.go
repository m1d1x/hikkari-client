package main

import (
	"bytes"
	"context"
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
