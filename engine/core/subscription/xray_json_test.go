package subscription

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func keepLink(link string) (string, error) { return link, nil }

const xrayConfigs = `[
 {"remarks":"Германия","outbounds":[
  {"protocol":"vless","tag":"srv-01","settings":{"vnext":[{"address":"a.example","port":443,"users":[{"id":"uuid-1","encryption":"none"}]}]},
   "streamSettings":{"network":"grpc","security":"tls","grpcSettings":{"serviceName":"svc"},"tlsSettings":{"serverName":"sni.example","fingerprint":"safari","alpn":["h2"]}}},
  {"protocol":"vless","tag":"srv-02","settings":{"vnext":[{"address":"second.example","port":443,"users":[{"id":"uuid-2"}]}]}},
  {"protocol":"freedom","tag":"direct"}]},
 {"remarks":"Reality","outbounds":[
  {"protocol":"vless","settings":{"vnext":[{"address":"b.example","port":8443,"users":[{"id":"uuid-3","flow":"xtls-rprx-vision"}]}]},
   "streamSettings":{"network":"tcp","security":"reality","realitySettings":{"serverName":"r.example","fingerprint":"firefox","publicKey":"pk","shortId":"ab"}}}]},
 {"remarks":"WS","outbounds":[
  {"protocol":"vless","settings":{"vnext":[{"address":"c.example","port":443,"users":[{"id":"uuid-4"}]}]},
   "streamSettings":{"network":"ws","security":"tls","wsSettings":{"path":"/p","headers":{"Host":"h.example"}},"tlsSettings":{"serverName":"h.example"}}}]},
 {"remarks":"Hy pinned","outbounds":[
  {"protocol":"hysteria","settings":{"address":"d.example","port":443},
   "streamSettings":{"security":"tls","hysteriaSettings":{"auth":"pw"},"tlsSettings":{"pinnedPeerCertSha256":"abc"}}}]},
 {"remarks":"Hy","outbounds":[
  {"protocol":"hysteria","settings":{"address":"e.example","port":443},
   "streamSettings":{"security":"tls","hysteriaSettings":{"auth":"pw"},"tlsSettings":{"serverName":"e.example"}}}]},
 {"remarks":"VMess","outbounds":[{"protocol":"vmess","settings":{}},{"protocol":"freedom"}]}
]`

func TestXrayConfigsBecomeOneLinkEach(t *testing.T) {
	links, err := Parse(xrayConfigs, "", keepLink)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 4 {
		t.Fatalf("links = %d: %q", len(links), links)
	}
	want := []struct {
		host, label string
		query       map[string]string
	}{
		{"a.example:443", "Германия", map[string]string{"type": "grpc", "serviceName": "svc", "security": "tls", "sni": "sni.example", "fp": "safari", "alpn": "h2"}},
		{"b.example:8443", "Reality", map[string]string{"flow": "xtls-rprx-vision", "security": "reality", "sni": "r.example", "fp": "firefox", "pbk": "pk", "sid": "ab"}},
		{"c.example:443", "WS", map[string]string{"type": "ws", "path": "/p", "host": "h.example"}},
		{"e.example:443", "Hy", map[string]string{"sni": "e.example"}},
	}
	for i, w := range want {
		u, err := url.Parse(links[i])
		if err != nil {
			t.Fatal(err)
		}
		if u.Host != w.host || u.Fragment != w.label {
			t.Errorf("link %d = %s#%s", i, u.Host, u.Fragment)
		}
		for key, value := range w.query {
			if got := u.Query().Get(key); got != value {
				t.Errorf("link %d %s = %q, want %q", i, key, got, value)
			}
		}
	}
}

func TestXrayConfigsWithNothingToDialReportNoServers(t *testing.T) {
	_, err := Parse(`[{"remarks":"x","outbounds":[{"protocol":"vmess"},{"protocol":"freedom"}]}]`, "", keepLink)
	if err == nil || !strings.Contains(err.Error(), "не найдено") {
		t.Fatalf("err = %v", err)
	}
}

func TestFetchRetriesWhenBouncedToAnotherHost(t *testing.T) {
	defer func(d time.Duration) { fetchRetryDelay = d }(fetchRetryDelay)
	fetchRetryDelay = time.Millisecond

	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<!doctype html>")
	}))
	defer elsewhere.Close()
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			http.Redirect(w, r, elsewhere.URL, http.StatusTemporaryRedirect)
			return
		}
		fmt.Fprint(w, "vless://id@a.example:443")
	}))
	defer srv.Close()

	decode := func(link string) (string, error) {
		if !strings.HasPrefix(link, "vless://") {
			return "", fmt.Errorf("not a link")
		}
		return link, nil
	}
	imported, err := Fetch(context.Background(), srv.URL, "", decode)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(imported.Items) != 1 {
		t.Fatalf("calls = %d, items = %d", calls, len(imported.Items))
	}
}
