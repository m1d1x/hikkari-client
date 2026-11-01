package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"NekkoVPN/core/svcproto"
)

type fakeStack struct {
	static []string
	resets int
}

func (f *fakeStack) StaticAdapters() ([]string, error) { return f.static, nil }
func (f *fakeStack) Reset() (svcproto.NetworkReset, error) {
	f.resets++
	return svcproto.NetworkReset{RebootRequired: true, Output: "ok", HostsDisabled: 2, DPIStopped: []string{"winws"}, DPIFailed: []string{"windivert"}}, nil
}
func (f *fakeStack) Scan() (svcproto.NetworkScan, error) {
	return svcproto.NetworkScan{HostsOverrides: 1, DPITools: []string{"goodbyedpi"}, ForeignWFP: []string{"ProtonVPN"}}, nil
}

// A static address is the one thing the reset destroys for good, so the
// service will not touch the stack — nor stop the tunnel — until the user
// confirmed it; a confirmed reset stops the core and drops the run marker.
func TestNetworkResetGuardsStaticAdapters(t *testing.T) {
	dir := t.TempDir()
	stack := &fakeStack{static: []string{"Ethernet"}}
	s, err := NewServer(Config{CoreDir: dir, CoreBin: "/nonexistent", CoreWorkDir: dir, NetworkStack: stack})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.runMarker(), []byte(svcproto.EngineSingBox), 0o644); err != nil {
		t.Fatal(err)
	}

	resp := s.dispatch(svcproto.Request{Op: svcproto.OpResetNetwork})
	if !resp.OK || resp.NetworkReset == nil || !resp.NetworkReset.Refused || stack.resets != 0 {
		t.Fatalf("unforced reset with a static adapter: %+v resets=%d", resp, stack.resets)
	}
	if _, err := os.Stat(s.runMarker()); err != nil {
		t.Fatalf("a refused reset dropped the run marker: %v", err)
	}

	resp = s.dispatch(svcproto.Request{Op: svcproto.OpResetNetwork, Force: true})
	if !resp.OK || resp.NetworkReset == nil || !resp.NetworkReset.RebootRequired || resp.NetworkReset.HostsDisabled != 2 || stack.resets != 1 {
		t.Fatalf("forced reset: %+v resets=%d", resp, stack.resets)
	}
	if _, err := os.Stat(s.runMarker()); !os.IsNotExist(err) {
		t.Fatalf("run marker survived the reset: %v", err)
	}
	if got := resp.NetworkReset; !slices.Equal(got.DPIStopped, []string{"winws"}) || !slices.Equal(got.DPIFailed, []string{"windivert"}) {
		t.Fatalf("dpi outcome lost on the way out: %+v", got)
	}
}

// The GUI tells "none found" from "could not check" by null against an empty
// list, which also covers a service too old to send the field.
func TestNetworkScanKeepsUnknownApartFromNone(t *testing.T) {
	unknown, err := json.Marshal(svcproto.NetworkScan{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(unknown), `"dpiTools":null`) || !strings.Contains(string(unknown), `"foreignWfp":null`) {
		t.Fatalf("unknown: %s", unknown)
	}
	none, err := json.Marshal(svcproto.NetworkScan{DPITools: []string{}, ForeignWFP: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(none), `"dpiTools":[]`) || !strings.Contains(string(none), `"foreignWfp":[]`) {
		t.Fatalf("none: %s", none)
	}
}

func TestNetworkResetIsRefusedWithoutAStack(t *testing.T) {
	dir := t.TempDir()
	s, err := NewServer(Config{CoreDir: dir, CoreBin: "/nonexistent", CoreWorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if resp := s.dispatch(svcproto.Request{Op: svcproto.OpResetNetwork}); resp.OK {
		t.Fatalf("reset without a stack: %+v", resp)
	}
}

type fakeRepair struct{ proxyOnly []bool }

func (f *fakeRepair) Repair(proxyOnly bool) (svcproto.NetworkRepair, error) {
	f.proxyOnly = append(f.proxyOnly, proxyOnly)
	return svcproto.NetworkRepair{Actions: []svcproto.NetworkAction{{Name: "proxy_disable", Done: true, Detail: "127.0.0.1:7890"}}}, nil
}

func TestNetworkRepairPassesTheScopeAndKeepsTheAddressOutOfTheLog(t *testing.T) {
	dir := t.TempDir()
	repair := &fakeRepair{}
	s, err := NewServer(Config{CoreDir: dir, CoreBin: "/nonexistent", CoreWorkDir: dir, HostRepair: repair})
	if err != nil {
		t.Fatal(err)
	}
	resp := s.dispatch(svcproto.Request{Op: svcproto.OpRepairNetwork, ProxyOnly: true})
	if !resp.OK || resp.NetworkRepair == nil || !resp.NetworkRepair.Actions[0].Done {
		t.Fatalf("repair: %+v", resp)
	}
	if !slices.Equal(repair.proxyOnly, []bool{true}) {
		t.Fatalf("scope: %v", repair.proxyOnly)
	}
	log, err := os.ReadFile(filepath.Join(dir, "service.log"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(log), "127.0.0.1") {
		t.Fatalf("the log names the proxy: %s", log)
	}
}

func TestNetworkRepairIsRefusedWhereTheGUIRepairs(t *testing.T) {
	dir := t.TempDir()
	s, err := NewServer(Config{CoreDir: dir, CoreBin: "/nonexistent", CoreWorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if resp := s.dispatch(svcproto.Request{Op: svcproto.OpRepairNetwork}); resp.OK {
		t.Fatal("repair ran without a platform side")
	}
}

func TestNetworkScanIsReadOnly(t *testing.T) {
	dir := t.TempDir()
	stack := &fakeStack{}
	s, err := NewServer(Config{CoreDir: dir, CoreBin: "/nonexistent", CoreWorkDir: dir, NetworkStack: stack})
	if err != nil {
		t.Fatal(err)
	}
	resp := s.dispatch(svcproto.Request{Op: svcproto.OpScanNetwork})
	if !resp.OK || resp.NetworkScan == nil || resp.NetworkScan.HostsOverrides != 1 || stack.resets != 0 {
		t.Fatalf("%+v resets=%d", resp, stack.resets)
	}
	if got := resp.NetworkScan; !slices.Equal(got.DPITools, []string{"goodbyedpi"}) || !slices.Equal(got.ForeignWFP, []string{"ProtonVPN"}) {
		t.Fatalf("scan lost on the way out: %+v", got)
	}
}
