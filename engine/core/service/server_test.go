//go:build darwin

package service

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
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
	if err := os.WriteFile(core, []byte("#!/bin/sh\nexec sleep 60\n"), 0o755); err != nil {
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
	dir := t.TempDir()
	core := filepath.Join(dir, "core")
	if err := os.WriteFile(core, []byte("#!/bin/sh\nexec sleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(Config{CoreDir: dir, CoreBin: core, CoreWorkDir: dir, CoreReady: fakeCoreUp})
	if err != nil {
		t.Fatal(err)
	}
	defer s.stopCores()
	restarted := func() bool {
		select {
		case <-s.restart:
			return true
		default:
			return false
		}
	}

	if resp := s.dispatch(svcproto.Request{Op: svcproto.OpStart, Config: json.RawMessage(`{"inbounds":[]}`)}); !resp.OK {
		t.Fatalf("start: %+v", resp)
	}
	s.requestRestartWhenIdle()
	if restarted() {
		t.Fatal("service restarted under a running core")
	}

	client, server := net.Pipe()
	go s.handle(server)
	if err := svcproto.WriteMessage(client, svcproto.Request{Op: svcproto.OpStop}); err != nil {
		t.Fatal(err)
	}
	var resp svcproto.Response
	if err := svcproto.ReadMessage(client, &resp); err != nil || !resp.OK {
		t.Fatalf("stop: %+v %v", resp, err)
	}
	client.Close()
	deadline := time.Now().Add(2 * time.Second)
	for !restarted() {
		if time.Now().After(deadline) {
			t.Fatal("service did not restart after the core stopped")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// With no core running the restart is immediate.
	s.restartWhenIdle.Store(false)
	s.requestRestartWhenIdle()
	if !restarted() {
		t.Fatal("idle service did not restart at once")
	}
}

// A core that cannot start must not leave the system resolver pointed at it.
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
	if err := os.WriteFile(core, []byte("#!/bin/sh\nexec sleep 60\n"), 0o755); err != nil {
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
	if err := os.WriteFile(core, []byte("#!/bin/sh\nexec sleep 60\n"), 0o755); err != nil {
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

// A service replaced by an update resumes the core on the file it holds. Status
// says so until the GUI sends a config, whichever release wrote the old one.
func TestStatusNamesTheReleaseThatWroteTheConfig(t *testing.T) {
	dir := t.TempDir()
	core := filepath.Join(dir, "core")
	if err := os.WriteFile(core, []byte("#!/bin/sh\nexec sleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(Config{CoreDir: dir, CoreBin: core, CoreWorkDir: dir, SystemDNS: &fakeDNS{}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.stopCores()

	if resp := s.dispatch(svcproto.Request{Op: svcproto.OpStart, Config: json.RawMessage(`{"inbounds":[]}`)}); !resp.OK {
		t.Fatalf("start: %+v", resp)
	}
	resp := s.dispatch(svcproto.Request{Op: svcproto.OpStatus})
	if !resp.ConfigTracked || resp.ConfigVersion != Version {
		t.Fatalf("after a start: tracked=%v config=%q, version %q", resp.ConfigTracked, resp.ConfigVersion, Version)
	}

	for name, written := range map[string]func() error{
		"older release": func() error { return os.WriteFile(s.configVersionPath(svcproto.EngineSingBox), []byte("1.0.0"), 0o644) },
		"no record":     func() error { return os.Remove(s.configVersionPath(svcproto.EngineSingBox)) },
	} {
		if err := written(); err != nil {
			t.Fatal(err)
		}
		if resp := s.dispatch(svcproto.Request{Op: svcproto.OpStatus}); resp.ConfigVersion == Version {
			t.Fatalf("%s reads as this release", name)
		}
		if err := os.WriteFile(s.configVersionPath(svcproto.EngineSingBox), []byte(Version), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
