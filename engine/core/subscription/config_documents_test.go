package subscription

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const clashConfig = `mixed-port: 7890
proxies:
  - name: Reality
    type: vless
    server: a.example
    port: 443
    uuid: uuid-1
    flow: xtls-rprx-vision
    tls: true
    servername: r.example
    client-fingerprint: firefox
    reality-opts: {public-key: pk, short-id: ab}
  - {name: WS, type: vmess, server: b.example, port: "8443", uuid: uuid-2, alterId: 0, cipher: auto, tls: true, servername: s.example, network: ws, ws-opts: {path: /p, headers: {Host: h.example}}}
  - {name: SS, type: ss, server: c.example, port: 8388, cipher: aes-128-gcm, password: pw}
  - {name: Hy, type: hysteria2, server: d.example, port: 443, password: pw, sni: d.example}
  - {name: Pinned, type: hysteria2, server: e.example, port: 443, password: pw, fingerprint: abc}
  - {name: Tuic, type: tuic, server: f.example, port: 443, uuid: uuid-3, password: pw, congestion-controller: bbr}
  - {name: Snell, type: snell, server: g.example, port: 443, psk: x}
proxy-groups:
  - {name: auto, type: select, proxies: [Reality]}
`

const singBoxConfig = `{"log":{},"outbounds":[
 {"type":"selector","tag":"select","outbounds":["a"]},
 {"type":"vless","tag":"gRPC","server":"a.example","server_port":443,"uuid":"uuid-1",
  "tls":{"enabled":true,"server_name":"s.example","utls":{"enabled":true,"fingerprint":"safari"},"alpn":["h2"]},
  "transport":{"type":"grpc","service_name":"svc"}},
 {"type":"vless","tag":"Reality","server":"b.example","server_port":443,"uuid":"uuid-2","flow":"xtls-rprx-vision",
  "tls":{"enabled":true,"server_name":"r.example","reality":{"enabled":true,"public_key":"pk","short_id":"ab"}}},
 {"type":"hysteria2","tag":"Hy","server":"c.example","server_port":443,"password":"pw","tls":{"enabled":true,"server_name":"c.example"}},
 {"type":"direct","tag":"direct"}
]}`

func linkOf(t *testing.T, links []string, label string) *url.URL {
	t.Helper()
	for _, link := range links {
		if u, err := url.Parse(link); err == nil && u.Fragment == label {
			return u
		}
	}
	t.Fatalf("no link %q in %q", label, links)
	return nil
}

func TestClashProxiesBecomeLinks(t *testing.T) {
	links, err := Parse(clashConfig, "", keepLink)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 5 {
		t.Fatalf("links = %d: %q", len(links), links)
	}
	reality := linkOf(t, links, "Reality").Query()
	if reality.Get("security") != "reality" || reality.Get("pbk") != "pk" || reality.Get("sid") != "ab" || reality.Get("flow") != "xtls-rprx-vision" || reality.Get("sni") != "r.example" {
		t.Fatalf("reality = %v", reality)
	}
	if u := linkOf(t, links, "SS"); u.Scheme != "ss" || u.Host != "c.example:8388" {
		t.Fatalf("ss = %v", u)
	}
	if u := linkOf(t, links, "Tuic"); u.Scheme != "tuic" || u.Query().Get("congestion_control") != "bbr" {
		t.Fatalf("tuic = %v", u)
	}
	var vmess string
	for _, link := range links {
		if strings.HasPrefix(link, "vmess://") {
			raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(link, "vmess://"))
			if err != nil {
				t.Fatal(err)
			}
			vmess = string(raw)
		}
	}
	for _, want := range []string{`"add":"b.example"`, `"port":8443`, `"net":"ws"`, `"host":"h.example"`, `"tls":"tls"`} {
		if !strings.Contains(vmess, want) {
			t.Errorf("vmess %s has no %s", vmess, want)
		}
	}
}

func TestSingBoxOutboundsBecomeLinks(t *testing.T) {
	links, err := Parse(singBoxConfig, "", keepLink)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 3 {
		t.Fatalf("links = %d: %q", len(links), links)
	}
	grpc := linkOf(t, links, "gRPC").Query()
	if grpc.Get("type") != "grpc" || grpc.Get("serviceName") != "svc" || grpc.Get("fp") != "safari" || grpc.Get("alpn") != "h2" {
		t.Fatalf("grpc = %v", grpc)
	}
	if reality := linkOf(t, links, "Reality").Query(); reality.Get("security") != "reality" || reality.Get("pbk") != "pk" {
		t.Fatalf("reality = %v", reality)
	}
}

func TestPlaceholderServersAreNeverImported(t *testing.T) {
	for name, input := range map[string]string{
		"zero uuid": "vless://00000000-0000-0000-0000-000000000000@real.example:443#x",
		"no host":   "vless://uuid@0.0.0.0:1#x",
		"remark":    "vless://uuid@real.example:443#" + url.PathEscape("Приложение не поддерживается"),
	} {
		if _, err := Parse(input, "", keepLink); !errors.Is(err, ErrUnsupportedClient) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	links, err := Parse("vless://uuid@0.0.0.0:1#x\nvless://uuid@real.example:443#y", "", keepLink)
	if err != nil || len(links) != 1 {
		t.Fatalf("links = %q, err = %v", links, err)
	}
}

func TestFetchAsksAsAnotherClientWhenRefused(t *testing.T) {
	var agents []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agent := r.Header.Get("User-Agent")
		agents = append(agents, agent)
		switch {
		case strings.HasPrefix(agent, "v2rayN"):
			http.NotFound(w, r)
		case len(agents) == 2:
			fmt.Fprint(w, "vless://00000000-0000-0000-0000-000000000000@0.0.0.0:1#stub")
		default:
			fmt.Fprint(w, "vless://uuid@a.example:443#real")
		}
	}))
	defer srv.Close()
	imported, err := Fetch(context.Background(), srv.URL, "", keepLink)
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 3 || len(imported.Items) != 1 {
		t.Fatalf("agents = %q, items = %q", agents, imported.Items)
	}
}

func TestFetchStaysOnTheFirstClientWhenItWorks(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, "vless://uuid@a.example:443#real")
	}))
	defer srv.Close()
	if _, err := Fetch(context.Background(), srv.URL, "", keepLink); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d", calls)
	}
}

func TestFetchDoesNotRetryThrottling(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	_, err := Fetch(context.Background(), srv.URL, "", keepLink)
	var status *HTTPError
	if !errors.As(err, &status) || status.Status != http.StatusTooManyRequests || calls != 1 {
		t.Fatalf("err = %v, calls = %d", err, calls)
	}
}
