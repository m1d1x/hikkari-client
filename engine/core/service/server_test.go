//go:build darwin

package service

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"NekkoVPN/core/engine"
	"NekkoVPN/core/engine/xray"
	"NekkoVPN/core/routing"
	"NekkoVPN/core/svcproto"
)

type fakeDNS struct {
	mu         sync.Mutex
	overridden []string
}

func (d *fakeDNS) Override(addr string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.overridden = append(d.overridden, addr)
	return nil
}

func (d *fakeDNS) Restore() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.overridden = nil
	return nil
}

func (d *fakeDNS) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.overridden)
}

// One core at a time: each runs with the system resolver pointed at it,
// starting sing-box takes Xray down, stopping gives the resolver back, and
// the run marker remembers which engine to resume.
func TestServiceRunsOneEngineAtATime(t *testing.T) {
	dir := t.TempDir()
	core := filepath.Join(dir, "core")
	if err := os.WriteFile(core, []byte(fakeCoreScript), 0o755); err != nil {
		t.Fatal(err)
	}
	dns := &fakeDNS{}
	s, err := NewServer(Config{CoreDir: dir, CoreBin: core, CoreWorkDir: dir, XrayBin: core, SystemDNS: dns, CoreReady: fakeCoreUp})
	if err != nil {
		t.Fatal(err)
	}
	defer s.stopCores()

	ps, err := xray.Decode("trojan://secret@example.com:443#T")
	if err != nil {
		t.Fatal(err)
	}
	ps[0].Name = "server"
	cfg, err := xray.CompileVPN(engine.Configuration{Profiles: ps, ActiveProfile: "server", Routing: routing.DefaultRoutingState()}, map[string]int{"server": 20808})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(cfg)

	resp := s.dispatch(svcproto.Request{Op: svcproto.OpStart, Engine: svcproto.EngineXray, Config: raw})
	if !resp.OK || resp.Engine != svcproto.EngineXray {
		t.Fatalf("xray start: %+v", resp)
	}
	if len(dns.overridden) != 1 || dns.overridden[0] != "172.31.7.1" {
		t.Fatalf("system DNS while Xray runs: %v", dns.overridden)
	}
	if marker, _ := os.ReadFile(s.runMarker()); string(marker) != svcproto.EngineXray {
		t.Fatalf("run marker %q", marker)
	}

	resp = s.dispatch(svcproto.Request{Op: svcproto.OpStart, Config: json.RawMessage(`{"inbounds":[]}`)})
	if !resp.OK || resp.Engine != svcproto.EngineSingBox {
		t.Fatalf("sing-box start: %+v", resp)
	}
	if s.cores[svcproto.EngineXray].Pid() != 0 {
		t.Fatal("Xray still runs beside sing-box")
	}
	if n := len(dns.overridden); n == 0 || dns.overridden[n-1] != svcproto.SingBoxDNS {
		t.Fatalf("system DNS while sing-box runs: %v", dns.overridden)
	}

	if resp = s.dispatch(svcproto.Request{Op: svcproto.OpStop}); !resp.OK || resp.Running {
		t.Fatalf("stop: %+v", resp)
	}
	if len(dns.overridden) != 0 {
		t.Fatalf("system DNS not given back: %v", dns.overridden)
	}
}

func TestServiceRefusesAnEngineItDoesNotShip(t *testing.T) {
	dir := t.TempDir()
	s, err := NewServer(Config{CoreDir: dir, CoreBin: "/bin/false", CoreWorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if resp := s.dispatch(svcproto.Request{Op: svcproto.OpStart, Engine: svcproto.EngineXray, Config: json.RawMessage(`{}`)}); resp.OK {
		t.Fatal("started Xray without a bundled binary")
	}
}

// Updated binaries synced while the core runs (the first connect after an
// update confirms the version) must not restart the service under the
// tunnel: the restart waits for the core to be stopped.
func TestBinaryRestartWaitsForTheCoreToStop(t *testing.T) {
	var offered []bool
	var mu sync.Mutex
	s := updateServer(t, Config{SyncBinaries: func(idle bool) bool {
		mu.Lock()
		defer mu.Unlock()
		offered = append(offered, idle)
		return idle
	}})
	restarted := func() bool {
		select {
		case <-s.restart:
			return true
		default:
			return false
		}
	}

	startCoreForTest(t, s, "h1")
	s.updates.settle(false)
	if restarted() {
		t.Fatal("service restarted under a running core")
	}

	// A stop for a restart hands nothing over: a start is about to follow.
	stop := func(req svcproto.Request) {
		t.Helper()
		client, server := net.Pipe()
		go s.handle(server)
		if err := svcproto.WriteMessage(client, req); err != nil {
			t.Fatal(err)
		}
		var resp svcproto.Response
		if err := svcproto.ReadMessage(client, &resp); err != nil || !resp.OK {
			t.Fatalf("stop: %+v %v", resp, err)
		}
		client.Close()
	}
	stop(svcproto.Request{Op: svcproto.OpStop, Restarting: true})
	time.Sleep(100 * time.Millisecond)
	if restarted() {
		t.Fatal("a stop for a restart restarted the service")
	}
	s.updates.settle(false)
	if restarted() {
		t.Fatal("the core is still wanted, yet the service restarted")
	}

	startCoreForTest(t, s, "h1")
	stop(svcproto.Request{Op: svcproto.OpStop})
	deadline := time.Now().Add(2 * time.Second)
	for !restarted() {
		if time.Now().After(deadline) {
			t.Fatal("service did not restart after the core stopped")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// A config this service refuses may need the new one, core or no core.
	if err := os.WriteFile(s.runMarker(), []byte(svcproto.EngineSingBox), 0o644); err != nil {
		t.Fatal(err)
	}
	if resp := s.dispatch(svcproto.Request{Op: svcproto.OpStart, Config: json.RawMessage(`{"inbounds":[],"unknown":1}`)}); resp.OK {
		t.Fatal("a config with an unknown section was accepted")
	}
	deadline = time.Now().Add(2 * time.Second)
	for !restarted() {
		if time.Now().After(deadline) {
			t.Fatal("a refused config did not hand over to the new binary")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// At start no core has run yet, whatever the marker says.
	if err := os.WriteFile(s.runMarker(), []byte(svcproto.EngineSingBox), 0o644); err != nil {
		t.Fatal(err)
	}
	if !s.updates.settle(true) || !restarted() {
		t.Fatal("a starting service did not take its new binary before the core")
	}
}

func TestFailedSingBoxStartGivesTheResolverBack(t *testing.T) {
	dir := t.TempDir()
	dns := &fakeDNS{}
	s, err := NewServer(Config{CoreDir: dir, CoreBin: filepath.Join(dir, "missing"), CoreWorkDir: dir, SystemDNS: dns})
	if err != nil {
		t.Fatal(err)
	}
	defer s.stopCores()
	if resp := s.dispatch(svcproto.Request{Op: svcproto.OpStart, Config: json.RawMessage(`{"inbounds":[]}`)}); resp.OK {
		t.Fatalf("started without a binary: %+v", resp)
	}
	if len(dns.overridden) != 0 {
		t.Fatalf("system DNS left overridden: %v", dns.overridden)
	}
}

// Where the platform catches DNS by routing there is no SystemDNS at all.
func TestSingBoxRunsWithoutSystemDNS(t *testing.T) {
	dir := t.TempDir()
	core := filepath.Join(dir, "core")
	if err := os.WriteFile(core, []byte(fakeCoreScript), 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(Config{CoreDir: dir, CoreBin: core, CoreWorkDir: dir, CoreReady: fakeCoreUp})
	if err != nil {
		t.Fatal(err)
	}
	defer s.stopCores()
	if resp := s.dispatch(svcproto.Request{Op: svcproto.OpStart, Config: json.RawMessage(`{"inbounds":[]}`)}); !resp.OK {
		t.Fatalf("start: %+v", resp)
	}
}

// A service that died with the override in place leaves it behind; the next
// one puts the user's setting back before anything else, also with no core to
// resume.
func TestNextStartRestoresTheResolverAfterACrash(t *testing.T) {
	dir := t.TempDir()
	dns := &fakeDNS{overridden: []string{svcproto.SingBoxDNS}}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(Config{CoreDir: dir, CoreBin: "/bin/false", CoreWorkDir: dir, SystemDNS: dns,
		Listen: func() (net.Listener, error) { return l, nil }})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for dns.count() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("override survived the restart")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
}

// A core that dies on its own takes the TUN with it: the override would point
// nowhere and the machine would stop resolving.
func TestDeadCoreGivesTheResolverBack(t *testing.T) {
	dir := t.TempDir()
	core := filepath.Join(dir, "core")
	if err := os.WriteFile(core, []byte(fakeCoreScript), 0o755); err != nil {
		t.Fatal(err)
	}
	dns := &fakeDNS{}
	s, err := NewServer(Config{CoreDir: dir, CoreBin: core, CoreWorkDir: dir, SystemDNS: dns, CoreReady: fakeCoreUp})
	if err != nil {
		t.Fatal(err)
	}
	defer s.stopCores()
	if resp := s.dispatch(svcproto.Request{Op: svcproto.OpStart, Config: json.RawMessage(`{"inbounds":[]}`)}); !resp.OK {
		t.Fatalf("start: %+v", resp)
	}
	s.releaseDeadCoreDNS()
	if dns.count() != 1 {
		t.Fatalf("running core lost the override: %v", dns.overridden)
	}
	proc, err := os.FindProcess(s.cores[svcproto.EngineSingBox].Pid())
	if err != nil {
		t.Fatal(err)
	}
	if err := proc.Kill(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for s.cores[svcproto.EngineSingBox].Pid() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("core still reported alive")
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.releaseDeadCoreDNS()
	if dns.count() != 0 {
		t.Fatalf("override outlived the core: %v", dns.overridden)
	}
}

// The fake cores below stand for a running core: they never open an API.
func fakeCoreUp() bool { return true }

// fakeCoreScript runs until killed and, like a real core, answers `run --help`
// at once (the start asks it whether the core has the hot reload channel).
const fakeCoreScript = "#!/bin/sh\n[ \"$2\" = \"--help\" ] && exit 0\nexec sleep 60\n"

// A core that gives up on its config exits at once. The start must say so, in
// the core's words, not report a running core (tester-002, 04.11).
func TestCoreThatExitsAtStartFailsTheStartWithItsReason(t *testing.T) {
	dir := t.TempDir()
	core := filepath.Join(dir, "core")
	script := "#!/bin/sh\nprintf '\\033[31mFATAL\\033[0m[0000] decode config at %s: endpoints[0].peers[0].preshared_key: json: unknown field \\\"preshared_key\\\"\\n' \"$3\" >&2\nexit 1\n"
	if err := os.WriteFile(core, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(Config{CoreDir: dir, CoreBin: core, CoreWorkDir: dir, CoreReady: func() bool { return false }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.stopCores()
	resp := s.dispatch(svcproto.Request{Op: svcproto.OpStart, Config: json.RawMessage(`{"inbounds":[]}`)})
	if resp.OK || resp.Error != `ядро остановилось при запуске: endpoints[0].peers[0].preshared_key: json: unknown field "preshared_key"` {
		t.Fatalf("start: ok=%v err=%q", resp.OK, resp.Error)
	}
	if _, err := os.Stat(s.runMarker()); err == nil {
		t.Fatal("a core that never ran is remembered as wanted up")
	}
}

// Status names what the running core was started on, and asks for nothing but the
// fingerprint the GUI itself sent: the service sees the config after pinning.
func TestStatusReportsWhatTheCoreWasStartedOn(t *testing.T) {
	s := updateServer(t, Config{})
	startCoreForTest(t, s, "h1")
	resp := s.dispatch(svcproto.Request{Op: svcproto.OpStatus})
	if !resp.Supervised || !resp.Wanted || !resp.Running || resp.ConfigHash != "h1" || resp.ConfigVersion != Version {
		t.Fatalf("after a start: %+v", resp)
	}
	if err := os.Remove(s.configHashPath(svcproto.EngineSingBox)); err != nil {
		t.Fatal(err)
	}
	if resp := s.dispatch(svcproto.Request{Op: svcproto.OpStatus}); resp.ConfigHash != "" {
		t.Fatalf("a config with no record reads as %q", resp.ConfigHash)
	}
	if resp := s.dispatch(svcproto.Request{Op: svcproto.OpStop, Restarting: true}); !resp.OK || !resp.Wanted || resp.Running {
		t.Fatalf("a stop for a restart must keep the wish: %+v", resp)
	}
	if resp := s.dispatch(svcproto.Request{Op: svcproto.OpStop}); !resp.OK || resp.Wanted {
		t.Fatalf("a stop must end the wish: %+v", resp)
	}
	if resp := s.dispatch(svcproto.Request{Op: svcproto.OpStart, Config: json.RawMessage(`{"inbounds":[]}`), ConfigHash: strings.Repeat("x", maxConfigHash+1)}); resp.OK {
		t.Fatal("an oversized fingerprint was stored")
	}
}
