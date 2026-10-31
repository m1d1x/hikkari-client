//go:build darwin

package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"NekkoVPN/core/engine"
	"NekkoVPN/core/engine/xray"
	"NekkoVPN/core/routing"
	"NekkoVPN/core/svcproto"
)

type fakeDNS struct{ overridden []string }

func (d *fakeDNS) Override(addr string) error { d.overridden = append(d.overridden, addr); return nil }
func (d *fakeDNS) Restore() error             { d.overridden = nil; return nil }

// One core at a time: Xray runs with the system resolver pointed at it,
// starting sing-box takes Xray down and gives the resolver back, and the
// run marker remembers which engine to resume.
func TestServiceRunsOneEngineAtATime(t *testing.T) {
	dir := t.TempDir()
	core := filepath.Join(dir, "core")
	if err := os.WriteFile(core, []byte("#!/bin/sh\nexec sleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	dns := &fakeDNS{}
	s, err := NewServer(Config{CoreDir: dir, CoreBin: core, CoreWorkDir: dir, XrayBin: core, SystemDNS: dns})
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
	if len(dns.overridden) != 0 {
		t.Fatalf("system DNS not given back: %v", dns.overridden)
	}

	if resp = s.dispatch(svcproto.Request{Op: svcproto.OpStop}); !resp.OK || resp.Running {
		t.Fatalf("stop: %+v", resp)
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
