package subscription

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"NekkoVPN/core/amnezia"
)

// The hub holds back what a released client's core would refuse, so a client
// that can dial AmneziaWG has to say so.
func TestFetchSaysWhatThisClientCanDial(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Hikkari-Features")
		fmt.Fprint(w, "x")
	}))
	defer srv.Close()
	_, _ = Fetch(context.Background(), srv.URL, "", func(s string) (string, error) { return s, nil })
	if got != "amneziawg" {
		t.Fatalf("Hikkari-Features = %q", got)
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
