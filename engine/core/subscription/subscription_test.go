package subscription

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"NekkoVPN/core/amnezia"
)

// The hub holds back what a released client's core would refuse, so a client
// that can dial AmneziaWG, and 3.1 on top, has to say so.
func TestFetchSaysWhatThisClientCanDial(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Hikkari-Features")
		fmt.Fprint(w, "x")
	}))
	defer srv.Close()
	_, _ = Fetch(context.Background(), srv.URL, "", func(s string) (string, error) { return s, nil })
	if got != "amneziawg, amneziawg-3.1, relay-metrics-v2" {
		t.Fatalf("Hikkari-Features = %q", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// The user's network goes to our hub and nowhere else: a third-party provider
// has no use for it.
func TestFetchTellsOnlyOurHubTheNetwork(t *testing.T) {
	oldNetwork, oldTransport := Network, http.DefaultTransport
	defer func() { Network, http.DefaultTransport = oldNetwork, oldTransport }()

	decode := func(s string) (string, error) { return s, nil }

	// A real provider over the wire: the header must not reach it.
	Network = func() string { return "AS12389" }
	var local []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		local = r.Header.Values(networkHeader)
		fmt.Fprint(w, "vless://u@h:443#n")
	}))
	defer srv.Close()
	if _, err := Fetch(context.Background(), srv.URL, "", decode); err != nil {
		t.Fatal(err)
	}
	if len(local) != 0 {
		t.Fatalf("a third-party provider got %s: %v", networkHeader, local)
	}

	// Our hub's name can't be served locally over TLS, so these stop at the
	// transport: what is under test is what the request carries.
	var got []string
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		got = r.Header.Values(networkHeader)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("vless://u@h:443#n")),
			Request:    r,
		}, nil
	})
	for _, tc := range []struct {
		url, network, want string
	}{
		{"https://sub.hikkari.net/x", "AS12389", "AS12389"},
		{"https://sub.hikkari.net/x", "", ""},
		{"https://hikkari.net.evil.com/x", "AS12389", ""},
		{"http://sub.hikkari.net/x", "AS12389", ""},
	} {
		got = nil
		Network = func() string { return tc.network }
		if _, err := Fetch(context.Background(), tc.url, "", decode); err != nil {
			t.Fatalf("%s: %v", tc.url, err)
		}
		switch {
		case tc.want == "" && len(got) != 0:
			t.Errorf("%s network %q: header sent: %v", tc.url, tc.network, got)
		case tc.want != "" && (len(got) != 1 || got[0] != tc.want):
			t.Errorf("%s network %q: header = %v, want %s", tc.url, tc.network, got, tc.want)
		}
	}
}

func TestOwnHub(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://sub.hikkari.net/x":      true,
		"https://SUB.Hikkari.NET/x":      true,
		"https://hikkari.net/x":          true,
		"https://sub.hikkari.net:8443/x": true,
		"https://hikkari.net.evil.com/x": false,
		"https://evilhikkari.net/x":      false,
		"https://hikkari.net@evil.com/x": false,
		"http://sub.hikkari.net/x":       false,
		"http://127.0.0.1:8080/x":        false,
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := ownHub(u); got != want {
			t.Errorf("ownHub(%s) = %v, want %v", raw, got, want)
		}
	}
}

func TestFetchRepeatsAroundTheTunnelWhenDefaultPathFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("vless://u@h:443#n"))
	}))
	defer srv.Close()

	var logged []string
	oldDial, oldLogf := DirectDial, Logf
	defer func() { DirectDial, Logf = oldDial, oldLogf }()
	Logf = func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }

	// The default path cannot reach the provider; only DirectDial can.
	dead := "http://127.0.0.1:1/"
	calls := 0
	DirectDial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		calls++
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	}
	_, err := fetchOnce(context.Background(), dead, "", "test", func(s string) ([]string, error) { return []string{s}, nil })
	if err != nil {
		t.Fatalf("fetch must succeed through DirectDial: %v", err)
	}
	if calls == 0 || len(logged) != 1 || !strings.Contains(logged[0], "answered") {
		t.Fatalf("calls=%d logged=%v", calls, logged)
	}

	DirectDial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return nil, errors.New("no physical interface")
	}
	logged = nil
	if _, err := fetchOnce(context.Background(), dead, "", "test", func(s string) ([]string, error) { return []string{s}, nil }); err == nil {
		t.Fatal("both paths down must fail")
	}
	if len(logged) != 1 || !strings.Contains(logged[0], "failed too") {
		t.Fatalf("logged=%v", logged)
	}
}

func TestPremiumKeyIsExchangedAtGateway(t *testing.T) {
	saved := fetchAmneziaConfig
	defer func() { fetchAmneziaConfig = saved }()
	var gotDevice string
	fetchAmneziaConfig = func(_ context.Context, key amnezia.ServiceKey, deviceID string) (string, error) {
		gotDevice = deviceID
		return "vpn://server-for-" + key.ServiceType, nil
	}
	key := amneziaServiceKeyLink(t)
	items, err := Parse(key, "device-1", func(link string) (string, error) { return link, nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0] != "vpn://server-for-amnezia-premium" || gotDevice != "device-1" {
		t.Fatalf("items=%v device=%q", items, gotDevice)
	}

	fetchAmneziaConfig = func(context.Context, amnezia.ServiceKey, string) (string, error) {
		return "", errors.New("gateway refused")
	}
	if _, err := Parse(key, "device-1", func(link string) (string, error) { return link, nil }); err == nil || err.Error() != "gateway refused" {
		t.Fatalf("the gateway's verdict must reach the user, got %v", err)
	}
}

func amneziaServiceKeyLink(t *testing.T) string {
	t.Helper()
	var compressed bytes.Buffer
	compressed.Write([]byte{0, 0, 0, 0})
	writer := zlib.NewWriter(&compressed)
	writer.Write([]byte(`{"api_config":{"service_type":"amnezia-premium"},"auth_data":{"api_key":"k"}}`))
	writer.Close()
	return "vpn://" + base64.RawURLEncoding.EncodeToString(compressed.Bytes())
}
