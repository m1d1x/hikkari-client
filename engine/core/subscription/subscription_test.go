package subscription

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
