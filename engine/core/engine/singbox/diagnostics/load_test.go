package diag

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoadTargetsTakeWhatACoreOfItsOwnCanRun(t *testing.T) {
	got, err := loadTargets([]byte(`{
		"outbounds": [
			{"tag":"p-a","type":"vless","server":"1.2.3.4","server_port":443,"tls":{"reality":{"enabled":true}},"domain_resolver":"dns-direct"},
			{"tag":"p-h","type":"hysteria2","server":"1.2.3.5","server_port":443},
			{"tag":"p-chain","type":"vless","server":"1.2.3.6","detour":"p-a"},
			{"tag":"proxy","type":"selector"},
			{"tag":"direct","type":"direct"}
		],
		"endpoints": [{"tag":"p-wg","type":"wireguard"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Tag != "p-a" || got[1].Tag != "p-h" {
		t.Fatalf("%+v", got)
	}
	if got[0].Proto != "vless-reality" {
		t.Fatalf("proto %q", got[0].Proto)
	}
	if _, kept := got[0].Outbound["domain_resolver"]; kept {
		t.Fatal("kept a resolver the core of its own does not have")
	}
}

func TestLoadTargetsHideABrokenConfig(t *testing.T) {
	_, err := loadTargets([]byte(`{"outbounds":[{"uuid":"SECRET"`))
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("%v", err)
	}
}

func TestDownloadSaysHowItEnded(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/full":
			_, _ = w.Write(make([]byte, 4096))
		case "/stall": // the curtain: the first bytes come, then silence
			w.Header().Set("Content-Length", "4096")
			_, _ = w.Write(make([]byte, 1024))
			w.(http.Flusher).Flush()
			select {
			case <-release:
			case <-r.Context().Done():
			}
		case "/cut":
			w.Header().Set("Content-Length", "4096")
			_, _ = w.Write(make([]byte, 1024))
			w.(http.Flusher).Flush()
			conn, _, _ := w.(http.Hijacker).Hijack()
			_ = conn.Close()
		case "/limited":
			w.WriteHeader(http.StatusTooManyRequests)
		}
	}))
	defer srv.Close()
	for _, c := range []struct {
		path string
		got  int64
		end  string
	}{
		{"/full", 4096, "ok"},
		{"/stall", 1024, "timeout"},
		{"/cut", 1024, "closed"},
		{"/limited", 0, "http429"},
	} {
		got, _, end := download(context.Background(), srv.Client(), srv.URL+c.path, 200*time.Millisecond)
		if got != c.got || end != c.end {
			t.Errorf("%s: got=%d end=%s", c.path, got, end)
		}
	}
}

func TestBurstCountsAnswersAndTimeouts(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	var seen atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch n := seen.Add(1); {
		case n <= 6:
			w.WriteHeader(http.StatusNoContent)
		case n <= 8: // a captive portal's 200 is not a working tunnel
			w.WriteHeader(http.StatusOK)
		default:
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}
	}))
	defer srv.Close()
	client := srv.Client()
	client.Timeout = 300 * time.Millisecond
	ok, timedOut, med := burst(context.Background(), client, srv.URL, 12)
	if ok != 6 || timedOut != 4 || med == "-" {
		t.Fatalf("ok=%d timeout=%d med=%s", ok, timedOut, med)
	}
}

func TestLoadLineHidesTheProfile(t *testing.T) {
	line := loadLine(context.Background(), "/nonexistent/sing-box",
		loadTarget{Tag: "p-a", Proto: "vless", Outbound: map[string]interface{}{"type": "vless", "server": "8.8.8.8", "uuid": "SECRET"}}, "lo", defaultLoadPlan)
	if strings.Contains(line, "SECRET") || !strings.Contains(line, `p-a proto=vless load_error=`) {
		t.Fatal(line)
	}
}

func TestLoadRunsOnlyWhenAllowed(t *testing.T) {
	r := New(discard{}, "/nonexistent")
	r.maybeLoad(context.Background())
	if !r.loadAt.IsZero() {
		t.Fatal("ran with no gate")
	}
	r.SetReachGate(func() bool { return false })
	r.maybeLoad(context.Background())
	if !r.loadAt.IsZero() {
		t.Fatal("ran with the gate shut")
	}
}

// TestLoadLineThroughARealCore runs a real core on both ends, since a config the
// core refuses passes every test above. Set HIKKARI_TEST_CORE to the binary.
func TestLoadLineThroughARealCore(t *testing.T) {
	binary := os.Getenv("HIKKARI_TEST_CORE")
	if binary == "" {
		t.Skip("HIKKARI_TEST_CORE is not set")
	}
	iface := "lo"
	if runtime.GOOS == "darwin" {
		iface = "lo0"
	}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/down" {
			_, _ = w.Write(make([]byte, loadBytes))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer origin.Close()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	const uuid = "bf000d23-0451-4b9c-8a7c-1d2e3f405162"
	server, _ := json.Marshal(map[string]interface{}{
		"log": map[string]interface{}{"disabled": true},
		"inbounds": []interface{}{map[string]interface{}{
			"type": "vless", "listen": "127.0.0.1", "listen_port": port,
			"users": []interface{}{map[string]interface{}{"uuid": uuid}},
		}},
		"outbounds": []interface{}{map[string]interface{}{"type": "direct", "tag": "out"}},
	})
	path := filepath.Join(t.TempDir(), "server.json")
	if err := os.WriteFile(path, server, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "run", "-c", path)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = cmd.Wait() }()
	if !waitPort(ctx, port) {
		t.Fatal("the server core did not come up")
	}

	plan := defaultLoadPlan
	plan.DownloadURL = origin.URL + "/down"
	plan.CheckURL = origin.URL + "/check"
	plan.BurstWait = 5 * time.Second
	plan.DownWait = 5 * time.Second
	target := loadTarget{Tag: "p-live", Proto: "vless", Outbound: map[string]interface{}{
		"tag": "p-live", "type": "vless", "server": "127.0.0.1", "server_port": port, "uuid": uuid,
	}}

	line := loadLine(ctx, binary, target, iface, plan)
	t.Log(line)
	want := fmt.Sprintf(`^p-live proto=vless dl=%d/%d took=\S+ end=ok burst=%d/%d timeout=0 med=\S+ after=%d/%d med=\S+$`,
		loadBytes, loadBytes, loadBurst, loadBurst, protoTries, protoTries)
	if !regexp.MustCompile(want).MatchString(line) {
		t.Fatalf("want %s, got %s", want, line)
	}

	// The server gone: the line must say so rather than read as healthy.
	cancel()
	_ = cmd.Wait()
	dead := loadLine(context.Background(), binary, target, iface, plan)
	t.Log(dead)
	if !strings.Contains(dead, " dl=0/") || !strings.Contains(dead, " burst=0/") || !strings.Contains(dead, " after=0/") {
		t.Fatalf("a dead server read as alive: %s", dead)
	}
}
