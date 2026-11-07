package subscription

import (
	"NekkoVPN/core/capacity"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestCapacityHeaderTrustedOnlyOnOwnSubscription(t *testing.T) {
	old := http.DefaultTransport
	defer func() { http.DefaultTransport = old }()
	link := "vless://u@relay.test:443?fp=chrome"
	metric := capacity.Metric{Key: capacity.Key(link), MeasuredAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Hour).Unix(), Mbps: 80, Points: 2}
	data, _ := json.Marshal([]capacity.Metric{metric})
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{capacity.Header: []string{string(data)}}, Body: io.NopCloser(strings.NewReader(link))}, nil
	})
	for _, test := range []struct {
		url  string
		want int
	}{{"https://sub.hikkari.net/sub/token", 1}, {"https://other.test/sub/token", 0}} {
		got, err := Fetch(context.Background(), test.url, "", func(s string) (string, error) { return s, nil })
		if err != nil || len(got.Metadata.Capacity) != test.want {
			t.Fatalf("%s: %+v %v", test.url, got.Metadata, err)
		}
	}
}

func TestFetchSealedMetricsWithoutTunnel(t *testing.T) {
	old := http.DefaultTransport
	defer func() { http.DefaultTransport = old }()
	link := "vless://u@relay.test:443?fp=chrome"
	now := time.Now().Unix()
	metrics := []capacity.Metric{{Key: capacity.Key(link), MeasuredAt: now, ExpiresAt: now + 5400, Mbps: 80, RTTMS: 120, Points: 2, History: []capacity.Point{{Ts: now / 1800 * 1800, RTTMS: 120, Mbps: 80}}}}
	payload, _ := json.Marshal(map[string]any{"version": 2, "links": []string{link}, "metrics": metrics})
	sealed, err := sealSubscription("token", payload)
	if err != nil {
		t.Fatal(err)
	}
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.Contains(r.Header.Get("Hikkari-Features"), "relay-metrics-v2") {
			t.Fatal("missing negotiation")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{metricsContentType}}, Body: io.NopCloser(bytes.NewReader(sealed))}, nil
	})
	got, err := Fetch(context.Background(), "https://sub.hikkari.net/sub/token", "", func(s string) (string, error) { return s, nil })
	if err != nil || len(got.Metadata.Capacity) != 1 || got.Metadata.Capacity[0].RTTMS != 120 || len(got.Metadata.Capacity[0].History) != 1 {
		t.Fatalf("lost sealed metrics: %+v %v", got, err)
	}
	if _, err := Fetch(context.Background(), "https://sub.hikkari.net/sub/wrong", "", func(s string) (string, error) { return s, nil }); err == nil {
		t.Fatal("wrong credential accepted")
	}
}
