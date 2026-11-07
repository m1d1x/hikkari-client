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
	"strconv"
	"strings"
	"sync"
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
	if strings.Contains(line, "SECRET") || !strings.Contains(line, `p-a proto=vless flow=none load_error=`) {
		t.Fatal(line)
	}
}

func TestLoadLineNamesTheKnobs(t *testing.T) {
	ob := map[string]interface{}{
		"type": "vless", "server": "8.8.8.8", "server_port": float64(8443), "uuid": "SECRET", "flow": "xtls-rprx-vision",
		"tls": map[string]interface{}{
			"enabled": true, "server_name": "www.samsung.com", "alpn": []interface{}{"h2", "http/1.1"},
			"utls":    map[string]interface{}{"enabled": true, "fingerprint": "firefox"},
			"reality": map[string]interface{}{"enabled": true, "public_key": "PUBKEY", "short_id": "SID"},
		},
	}
	got := loadKnobs(ob)
	if want := " fp=firefox sni=www.samsung.com port=8443 flow=xtls-rprx-vision alpn=h2/http/1.1"; got != want {
		t.Fatalf("knobs %q, want %q", got, want)
	}
	line := loadLine(context.Background(), "/nonexistent/sing-box", loadTarget{Tag: "p-a", Proto: "vless-reality", Outbound: ob}, "lo", defaultLoadPlan)
	for _, secret := range []string{"SECRET", "8.8.8.8", "PUBKEY", "SID"} {
		if strings.Contains(line, secret) {
			t.Fatalf("line names %s: %s", secret, line)
		}
	}
	if !strings.HasPrefix(line, "p-a proto=vless-reality fp=firefox sni=www.samsung.com port=8443 ") {
		t.Fatal(line)
	}
	if got := loadKnobs(map[string]interface{}{"type": "hysteria2", "tls": map[string]interface{}{"server_name": "a b"}}); got != "" {
		t.Fatalf("hysteria with a bad name: %q", got)
	}
}

// silentListener accepts TCP and never says a word: what a banned address looks
// like from outside.
func silentListener(t *testing.T) (string, func()) {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		var held []net.Conn
		defer func() {
			for _, c := range held {
				_ = c.Close()
			}
		}()
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			held = append(held, c)
			select {
			case <-done:
				return
			default:
			}
		}
	}()
	return l.Addr().String(), func() { close(done); _ = l.Close() }
}

func TestHelloFlowsTellSilenceFromAnAnswer(t *testing.T) {
	local := net.IPv4(127, 0, 0, 1)
	silent, stop := silentListener(t)
	defer stop()
	if opened, answered := helloFlows(context.Background(), local, silent, "www.example.com", 200*time.Millisecond); opened != addrFlows || answered != 0 {
		t.Fatalf("silent: opened=%d answered=%d", opened, answered)
	}

	tlsSrv := httptest.NewTLSServer(http.NotFoundHandler())
	defer tlsSrv.Close()
	if opened, answered := helloFlows(context.Background(), local, tlsSrv.Listener.Addr().String(), "www.example.com", 2*time.Second); opened != addrFlows || answered != addrFlows {
		t.Fatalf("tls: opened=%d answered=%d", opened, answered)
	}

	// A server that closes on sight has answered: it is there.
	closing, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer closing.Close()
	go func() {
		for {
			c, err := closing.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	if _, answered := helloFlows(context.Background(), local, closing.Addr().String(), "", 2*time.Second); answered != addrFlows {
		t.Fatalf("closing: answered=%d", answered)
	}
}

func TestLoadLineOnADeadAddressMeasuresNothing(t *testing.T) {
	addr, stop := silentListener(t)
	defer stop()
	host, port, _ := net.SplitHostPort(addr)
	p, _ := strconv.Atoi(port)
	ob := map[string]interface{}{
		"type": "vless", "server": host, "server_port": float64(p), "uuid": "SECRET",
		"tls": map[string]interface{}{"enabled": true, "server_name": "www.samsung.com"},
	}
	plan := defaultLoadPlan
	plan.Local = net.IPv4(127, 0, 0, 1)
	start := time.Now()
	line := loadLine(context.Background(), "/nonexistent/sing-box", loadTarget{Tag: "p-a", Proto: "vless", Outbound: ob}, "lo", plan)
	if !strings.Contains(line, " addr_dead=1 tcp=12/12 reply=0/12") || strings.Contains(line, "burst=") || strings.Contains(line, "load_error") {
		t.Fatal(line)
	}
	if strings.Contains(line, "SECRET") || strings.Contains(line, host) {
		t.Fatalf("names the profile: %s", line)
	}
	if took := time.Since(start); took > addrWait+5*time.Second {
		t.Fatalf("took %s", took)
	}
}

func TestAddrDeadAsksOnlyWhatCanAnswer(t *testing.T) {
	addr, stop := silentListener(t)
	defer stop()
	host, port, _ := net.SplitHostPort(addr)
	p, _ := strconv.Atoi(port)
	local := net.IPv4(127, 0, 0, 1)
	for name, ob := range map[string]map[string]interface{}{
		"no TLS: it would stay silent alive": {"type": "shadowsocks", "server": host, "server_port": p},
		"UDP: there is no TCP to ask":        {"type": "hysteria2", "server": host, "server_port": p, "tls": map[string]interface{}{"enabled": true}},
		"a name that does not resolve":       {"type": "vless", "server": "no-such-host.invalid", "server_port": p, "tls": map[string]interface{}{"enabled": true}},
	} {
		if _, dead := addrDead(context.Background(), loadTarget{Tag: "p-a", Outbound: ob}, local); dead {
			t.Errorf("%s: read as dead", name)
		}
	}
	if _, dead := addrDead(context.Background(), loadTarget{Tag: "p-a", Outbound: map[string]interface{}{"type": "vless", "server": host, "server_port": p, "tls": map[string]interface{}{"enabled": true}}}, nil); dead {
		t.Error("no card to ask from: read as dead")
	}
}

func TestOnMobile(t *testing.T) {
	for _, c := range []struct {
		iface, network string
		want           bool
	}{
		{"Ethernet", "AS31133", true},
		{"Wi-Fi", "AS16345", true},
		{"Cellular", "unknown", true},
		{"Мобильная сеть", "unknown", true},
		{"Ethernet", "unknown", false},
		{"Ethernet", "AS12389", false},
	} {
		if got := onMobile(c.iface, c.network); got != c.want {
			t.Errorf("%s %s: %v", c.iface, c.network, got)
		}
	}
}

func stubSelfNet(t *testing.T, network string) {
	t.Helper()
	was := loadSelfNet
	loadSelfNet = func(net.IP, []string) string { return network }
	t.Cleanup(func() { loadSelfNet = was })
}

func TestLoadOnAMobileNetworkAsksASmallBurstAndNotOften(t *testing.T) {
	defer func(os string) { loadOS = os }(loadOS)
	loadOS = "windows"
	stubSelfNet(t, "AS31133")
	config := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(config, []byte(`{"outbounds":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	r := New(discard{}, config)
	ifaces := []physIface{{Name: "lo"}}
	if got := r.loadNow("/nonexistent/sing-box", ifaces); len(got) != 1 || !strings.HasPrefix(got[0], "started") {
		t.Fatalf("first round: %v", got)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		r.mu.Lock()
		idle, mobile := !r.loadBusy, r.loadMobile
		r.mu.Unlock()
		if idle && mobile {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the round did not find the mobile network")
		}
		time.Sleep(5 * time.Millisecond)
	}
	got := r.loadNow("/nonexistent/sing-box", ifaces)
	if len(got) != 1 || !strings.Contains(got[0], "mobile network") {
		t.Fatalf("a second round within the limit: %v", got)
	}
	r.mu.Lock()
	r.loadAt = time.Now().Add(-loadEvery - time.Minute)
	r.mu.Unlock()
	if got := r.loadNow("/nonexistent/sing-box", ifaces); !strings.HasPrefix(got[0], "started") {
		t.Fatalf("after the limit: %v", got)
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
	want := fmt.Sprintf(`^p-live proto=vless port=\d+ flow=none dl=%d/%d took=\S+ end=ok burst=%d/%d timeout=0 med=\S+ after=%d/%d med=\S+$`,
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

type lines struct {
	mu  sync.Mutex
	got []string
}

func (l *lines) Logf(format string, args ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.got = append(l.got, fmt.Sprintf(format, args...))
}

func (l *lines) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.got...)
}

func TestLoadNowAnswersAtOnceAndClosesTheRound(t *testing.T) {
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

	if got := r.loadNow("/nonexistent/sing-box", ifaces); len(got) != 1 || got[0] != "started profiles=2" {
		t.Fatalf("%v", got)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		got := log.snapshot()
		if len(got) > 0 && got[len(got)-1] == "[diag:load] on request: done" {
			// One line a profile: the hello is the release test's, not the round's.
			if len(got) != 3 || !strings.HasPrefix(got[0], "[diag:load] p-a proto=") || !strings.HasPrefix(got[1], "[diag:load] p-b proto=") {
				t.Fatalf("%q", got)
			}
			if strings.Contains(strings.Join(got, "\n"), "SECRET") {
				t.Fatal("a secret reached the log")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no closing line: %q", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if time.Since(r.loadAt) > time.Minute {
		t.Fatal("the next periodic round does not count from the request")
	}
}

func TestLoadNowRefusesASecondRound(t *testing.T) {
	defer func(os string) { loadOS = os }(loadOS)
	loadOS = "windows"
	config := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(config, []byte(`{"outbounds":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	r := New(discard{}, config)
	r.loadBusy = true
	if got := r.loadNow("/nonexistent/sing-box", []physIface{{Name: "lo"}}); len(got) != 1 || !strings.HasPrefix(got[0], "error=") {
		t.Fatalf("%v", got)
	}
}

func TestLoadNowSaysWhyItCannotRun(t *testing.T) {
	defer func(os string) { loadOS = os }(loadOS)
	loadOS = "darwin"
	r := New(discard{}, "/nonexistent")
	if got := r.loadNow("/nonexistent/sing-box", nil); len(got) != 1 || !strings.Contains(got[0], "needs Windows") {
		t.Fatalf("%v", got)
	}
	loadOS = "windows"
	if got := r.loadNow("/nonexistent/sing-box", nil); len(got) != 1 || !strings.Contains(got[0], "no physical interface") {
		t.Fatalf("%v", got)
	}
}
