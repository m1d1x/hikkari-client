package foreignvpn

import (
	"errors"
	"slices"
	"testing"
)

type fakeSystem struct {
	procs    []Process
	services []Service
	vpns     []SystemVPN
	fail     map[string]bool // by executable name, service name or VPN ID
	calls    []string
}

func (f *fakeSystem) Processes() ([]Process, error)       { return f.procs, nil }
func (f *fakeSystem) Services() ([]Service, error)        { return f.services, nil }
func (f *fakeSystem) ConnectedVPNs() ([]SystemVPN, error) { return f.vpns, nil }

func (f *fakeSystem) result(key string) error {
	f.calls = append(f.calls, key)
	if f.fail[key] {
		return errors.New("refused")
	}
	return nil
}
func (f *fakeSystem) StopService(name string) error { return f.result("svc:" + name) }
func (f *fakeSystem) KillProcess(p Process) error   { return f.result("kill:" + exeName(p.Path)) }
func (f *fakeSystem) StopVPN(v SystemVPN) error     { return f.result("vpn:" + v.ID) }

func nolog(string, ...any) {}

// The machine of tester-004: AmneziaVPN connected over AmneziaWG, its
// daemon beside it, and Hikkari's own sing-box.
func TestStopsAmneziaTunnelAndAppNotDaemon(t *testing.T) {
	sys := &fakeSystem{procs: []Process{
		{305, "/Applications/AmneziaVPN.app/Contents/MacOS/AmneziaVPN-service"},
		{581, "/Applications/AmneziaVPN.app/Contents/MacOS/AmneziaVPN"},
		{50668, "/Applications/AmneziaVPN.app/Contents/MacOS/amneziawg-go"},
		{54894, "/Applications/hikkari.app/Contents/Resources/sing-box"},
	}}
	out, err := Stop(sys, nolog)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(out.Stopped, []string{"AmneziaVPN"}) || len(out.Failed) != 0 {
		t.Fatalf("outcome %+v", out)
	}
	// The app goes before the tunnel, so it cannot bring it back.
	if want := []string{"kill:amneziavpn", "kill:amneziawg-go"}; !slices.Equal(sys.calls, want) {
		t.Fatalf("calls %v, want %v", sys.calls, want)
	}
}

func TestLeavesDisconnectedClientAlone(t *testing.T) {
	sys := &fakeSystem{
		procs: []Process{
			{581, "/Applications/AmneziaVPN.app/Contents/MacOS/AmneziaVPN"},
			{2, `C:\Program Files\WireGuard\wireguard.exe`},
		},
		services: []Service{{Name: "WireGuardTunnel$home", Running: false}, {Name: "WireGuardManager", Running: true}},
	}
	out, err := Stop(sys, nolog)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Stopped) != 0 || len(out.Failed) != 0 || len(sys.calls) != 0 {
		t.Fatalf("outcome %+v, calls %v", out, sys.calls)
	}
}

func TestStopsWindowsTunnelServices(t *testing.T) {
	sys := &fakeSystem{
		procs: []Process{{7, `C:\Program Files\WireGuard\wireguard.exe`}},
		services: []Service{
			{Name: "WireGuardTunnel$office", Running: true},
			{Name: "AmneziaWGTunnel$AmneziaVPN", Running: true},
			{Name: "WireGuardTunnel$", Running: true}, // no tunnel name: not the scheme
		},
	}
	out, err := Stop(sys, nolog)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(out.Stopped, []string{"AmneziaVPN", "WireGuard"}) {
		t.Fatalf("outcome %+v", out)
	}
	want := []string{"svc:AmneziaWGTunnel$AmneziaVPN", "kill:wireguard", "svc:WireGuardTunnel$office"}
	if !slices.Equal(sys.calls, want) {
		t.Fatalf("calls %v, want %v", sys.calls, want)
	}
}

func TestFailedPartKeepsClientInFailedAndGoesOn(t *testing.T) {
	sys := &fakeSystem{
		procs: []Process{
			{1, "/x/amneziawg-go"},
			{2, "/usr/local/sbin/openvpn"},
		},
		vpns: []SystemVPN{{ID: "A1", Name: "Работа"}, {ID: "B2", Name: "Outline"}},
		fail: map[string]bool{"kill:amneziawg-go": true, "vpn:B2": true},
	}
	out, err := Stop(sys, nolog)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(out.Stopped, []string{"OpenVPN", "Работа"}) || !slices.Equal(out.Failed, []string{"AmneziaVPN", "Outline"}) {
		t.Fatalf("outcome %+v", out)
	}
}

type brokenSystem struct{ fakeSystem }

func (brokenSystem) Processes() ([]Process, error) { return nil, errors.New("no listing") }

func TestUnreadableMachineChangesNothing(t *testing.T) {
	sys := &brokenSystem{}
	if _, err := Stop(sys, nolog); err == nil {
		t.Fatal("want error")
	}
	if len(sys.calls) != 0 {
		t.Fatalf("calls %v", sys.calls)
	}
}
