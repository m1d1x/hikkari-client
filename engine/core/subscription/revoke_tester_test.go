package subscription

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func decodeLink(s string) (string, error) { return s, nil }

// Our hub says tester inside the sealed body; a header saying so, or another
// provider's body, is not believed.
func TestTheTesterFlagComesOnlyFromOurSealedBody(t *testing.T) {
	old := http.DefaultTransport
	defer func() { http.DefaultTransport = old }()
	link := "vless://u@relay.test:443?fp=chrome"
	sealedAs := func(tester bool) []byte {
		payload, _ := json.Marshal(map[string]any{"version": 2, "links": []string{link}, "metrics": []any{}, "tester": tester})
		sealed, err := sealSubscription("token", payload)
		if err != nil {
			t.Fatal(err)
		}
		return sealed
	}
	var body []byte
	var header http.Header
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(bytes.NewReader(body))}, nil
	})

	body, header = sealedAs(true), http.Header{"Content-Type": []string{metricsContentType}}
	got, err := Fetch(context.Background(), "https://sub.hikkari.net/sub/token", "", decodeLink)
	if err != nil || !got.Metadata.Tester {
		t.Fatalf("a tester's sealed subscription: %+v %v", got.Metadata, err)
	}
	body = sealedAs(false)
	if got, err := Fetch(context.Background(), "https://sub.hikkari.net/sub/token", "", decodeLink); err != nil || got.Metadata.Tester {
		t.Fatalf("an ordinary subscription read as a tester's: %+v %v", got.Metadata, err)
	}
	body, header = []byte(link), http.Header{"Tester": []string{"true"}, "Hikkari-Tester": []string{"1"}}
	if got, err := Fetch(context.Background(), "https://sub.hikkari.net/sub/token", "", decodeLink); err != nil || got.Metadata.Tester {
		t.Fatalf("a plain body with a header made a tester: %+v %v", got.Metadata, err)
	}
}

// Only our hub's 410 marked revoked ends a subscription; another provider's
// 410, ours without the mark, or a 404 is an ordinary failure.
func TestOnlyOurHubsMarkedGoneIsARevoke(t *testing.T) {
	old := http.DefaultTransport
	defer func() { http.DefaultTransport = old }()
	var status int
	var header http.Header
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader("gone"))}, nil
	})
	marked := http.Header{"Hikkari-Subscription": []string{"revoked"}}
	for _, c := range []struct {
		url     string
		status  int
		header  http.Header
		revoked bool
	}{
		{"https://sub.hikkari.net/sub/token", http.StatusGone, marked, true},
		{"https://sub.hikkari.net/sub/token", http.StatusGone, http.Header{}, false},
		{"https://sub.hikkari.net/sub/token", http.StatusNotFound, marked, false},
		{"https://provider.test/sub/token", http.StatusGone, marked, false},
	} {
		status, header = c.status, c.header
		_, err := Fetch(context.Background(), c.url, "", decodeLink)
		if errors.Is(err, ErrRevoked) != c.revoked {
			t.Fatalf("%s %d %v: %v, want revoked %v", c.url, c.status, c.header, err, c.revoked)
		}
		if err == nil {
			t.Fatalf("%s %d: no error", c.url, c.status)
		}
	}
}
