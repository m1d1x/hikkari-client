package diag

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// speedServer stands in for Cloudflare's speed endpoints: an endless download
// and an upload that reads what it is sent.
func speedServer(t *testing.T) (*httptest.Server, speedPlan) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/__down", func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 32<<10)
		for r.Context().Err() == nil {
			if _, err := w.Write(buf); err != nil {
				return
			}
		}
	})
	mux.HandleFunc("/__up", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
	})
	mux.HandleFunc("/__refuse", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, speedPlan{DownURL: srv.URL + "/__down", UpURL: srv.URL + "/__up", Streams: 3, Window: 1500 * time.Millisecond}
}

func TestSpeedMeasuresBothDirectionsOverTheWindow(t *testing.T) {
	_, plan := speedServer(t)
	client := &http.Client{}
	for name, run := range map[string]func() speedResult{
		"down": func() speedResult { return speedDown(context.Background(), client, plan) },
		"up":   func() speedResult { return speedUp(context.Background(), client, plan) },
	} {
		start := time.Now()
		got := run()
		if took := time.Since(start); took > plan.Window+time.Second {
			t.Fatalf("%s: the window is %s and the round took %s", name, plan.Window, took)
		}
		if got.bytes == 0 || got.failed != 0 {
			t.Fatalf("%s: %+v", name, got)
		}
		if len(got.perSec) == 0 {
			t.Fatalf("%s: no second was sampled", name)
		}
		if s := got.String(); !strings.Contains(s, "Mbps(peak=") || !strings.Contains(s, "fail=0") {
			t.Fatalf("%s: %q", name, s)
		}
	}
}

func TestSpeedSaysWhyADirectionMovedNothing(t *testing.T) {
	srv, plan := speedServer(t)
	plan.DownURL = srv.URL + "/__refuse"
	plan.Window = 700 * time.Millisecond
	got := speedDown(context.Background(), &http.Client{}, plan)
	if got.bytes != 0 || got.failed == 0 {
		t.Fatalf("%+v", got)
	}
	// A refusing path is answered every 200 ms a flow, not spun on.
	if got.failed > plan.Streams*5 {
		t.Fatalf("a refused flow spins: %d failures", got.failed)
	}
	if s := got.String(); !strings.HasPrefix(s, "0(fail=") || !strings.Contains(s, "end=http403") {
		t.Fatalf("%q", s)
	}
}

func TestSpeedNowRunsOnlyTheNamedProfile(t *testing.T) {
	defer func(os string) { loadOS = os }(loadOS)
	loadOS = "windows"
	stubSelfNet(t, "unknown")
	config := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(config, []byte(`{"outbounds":[
		{"tag":"p-a","type":"vless","server":"8.8.8.8","uuid":"SECRET"},
		{"tag":"p-b","type":"trojan","server":"8.8.4.4","password":"SECRET"}
	]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	log := &lines{}
	r := New(log, config)
	ifaces := []physIface{{Name: "lo"}}
	_, plan := speedServer(t)

	got := r.speedNow("/nonexistent/sing-box", ifaces, json.RawMessage(`{"profile":"p-b"}`), plan)
	if len(got) != 1 || !strings.HasPrefix(got[0], "started profiles=1 ") {
		t.Fatalf("%v", got)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		lines := log.snapshot()
		if len(lines) > 0 && lines[len(lines)-1] == "[diag:speed] on request: done" {
			if len(lines) != 2 || !strings.HasPrefix(lines[0], "[diag:speed] p-b proto=") {
				t.Fatalf("%q", lines)
			}
			if strings.Contains(strings.Join(lines, "\n"), "SECRET") {
				t.Fatal("a secret reached the log")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no closing line: %q", lines)
		}
		time.Sleep(10 * time.Millisecond)
	}
	// A speed round leaves the periodic load round's clock alone.
	if !r.loadAt.IsZero() {
		t.Fatal("the speed round moved the load round's clock")
	}
}

func TestSpeedNowRefuses(t *testing.T) {
	defer func(os string) { loadOS = os }(loadOS)
	loadOS = "windows"
	config := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(config, []byte(`{"outbounds":[{"tag":"p-a","type":"vless","server":"8.8.8.8"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ifaces := []physIface{{Name: "lo"}}
	_, plan := speedServer(t)
	for name, c := range map[string]struct {
		params string
		busy   bool
		want   string
	}{
		"not a tag":     {params: `{"profile":"../x"}`, want: "not a profile tag"},
		"no such tag":   {params: `{"profile":"p-zz"}`, want: "no profile p-zz"},
		"not readable":  {params: `[1]`, want: "not readable"},
		"a round is on": {busy: true, want: "is running"},
	} {
		r := New(discard{}, config)
		r.loadBusy = c.busy
		got := r.speedNow("/nonexistent/sing-box", ifaces, json.RawMessage(c.params), plan)
		if len(got) != 1 || !strings.HasPrefix(got[0], "error=") || !strings.Contains(got[0], c.want) {
			t.Fatalf("%s: %v", name, got)
		}
	}
}

func TestSpeedOnAMobileNetworkDoesNotRun(t *testing.T) {
	defer func(os string) { loadOS = os }(loadOS)
	loadOS = "windows"
	stubSelfNet(t, "AS31133")
	config := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(config, []byte(`{"outbounds":[{"tag":"p-a","type":"vless","server":"8.8.8.8"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	log := &lines{}
	r := New(log, config)
	_, plan := speedServer(t)
	if got := r.speedNow("/nonexistent/sing-box", []physIface{{Name: "lo"}}, nil, plan); !strings.HasPrefix(got[0], "started") {
		t.Fatalf("%v", got)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		lines := log.snapshot()
		if len(lines) == 2 && strings.Contains(lines[0], "skipped: mobile network") && lines[1] == "[diag:speed] on request: done" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%q", lines)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// 1.0.95 asked for 100 << 20 bytes a request and Cloudflare refused every one
// with 403: the round measured no download at all.
func TestSpeedAsksCloudflareForNoMoreThanItServes(t *testing.T) {
	if speedDownBytes >= cloudflareDownLimit {
		t.Fatalf("a download request of %d bytes; Cloudflare refuses %d and more", speedDownBytes, cloudflareDownLimit)
	}
	if !strings.Contains(defaultSpeedPlan.DownURL, fmt.Sprintf("bytes=%d", speedDownBytes)) {
		t.Fatalf("the plan asks %q", defaultSpeedPlan.DownURL)
	}
	if loadBytes >= cloudflareDownLimit {
		t.Fatalf("diag:load asks %d bytes", loadBytes)
	}
}
