//go:build darwin

package macsvc

import (
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

// fakeNet is one machine: the primary device, what DHCP gave each device and
// which resolvers answer through it.
type fakeNet struct {
	primaryDev string
	dhcp       map[string][]string
	alive      map[string]bool // "device server"
	tunnel     bool
}

func (f *fakeNet) services() ([]serviceDevice, error) {
	return []serviceDevice{{"Wi-Fi", "en0"}, {"iPhone USB", "en6"}}, nil
}
func (f *fakeNet) primary() string                         { return f.primaryDev }
func (f *fakeNet) hasIPv4(device string) bool              { return len(f.dhcp[device]) > 0 }
func (f *fakeNet) networkResolvers(device string) []string { return f.dhcp[device] }
func (f *fakeNet) answers(device, server string) bool      { return f.alive[device+" "+server] }
func (f *fakeNet) tunnelHoldsDNS() bool                    { return f.tunnel }

// tethered: the phone routes packets, its own DNS forwarder is dead.
func tethered() *fakeNet {
	return &fakeNet{
		primaryDev: "en6",
		dhcp:       map[string][]string{"en6": {"172.20.10.1"}},
		alive:      map[string]bool{"en6 77.88.8.8": true, "en6 77.88.8.1": true},
	}
}

func newFallback(t *testing.T, env *fakeNet, settings fakeSettings) dnsFallback {
	return dnsFallback{dir: filepath.Join(t.TempDir(), "fallback"), net: settings, env: env, mu: &sync.Mutex{}}
}

func TestDeadNetworkResolverGetsTheFallbackAndBackWhenItRecovers(t *testing.T) {
	env, settings := tethered(), fakeSettings{}
	f := newFallback(t, env, settings)
	if a := f.Check(); !a.Done {
		t.Fatalf("fallback not set: %+v", a)
	}
	if got := settings["iPhone USB"]; !reflect.DeepEqual(got, fallbackServers) {
		t.Fatalf("iPhone USB DNS = %v", got)
	}
	// Still dead: nothing changes.
	if a := f.Check(); a.Done {
		t.Fatalf("changed again: %+v", a)
	}
	// The phone's forwarder answers again: back to DHCP.
	env.alive["en6 172.20.10.1"] = true
	if a := f.Check(); !a.Done {
		t.Fatalf("not handed back: %+v", a)
	}
	if _, ok := settings["iPhone USB"]; ok {
		t.Fatalf("still manual: %v", settings["iPhone USB"])
	}
}

func TestWorkingResolverIsLeftAlone(t *testing.T) {
	env, settings := tethered(), fakeSettings{}
	env.alive["en6 172.20.10.1"] = true
	if a := newFallback(t, env, settings).Check(); a.Done || len(settings) != 0 {
		t.Fatalf("changed a working network: %+v %v", a, settings)
	}
}

func TestUsersManualDNSIsNeverReplaced(t *testing.T) {
	env, settings := tethered(), fakeSettings{"iPhone USB": {"9.9.9.9"}}
	newFallback(t, env, settings).Check()
	if got := settings["iPhone USB"]; !reflect.DeepEqual(got, []string{"9.9.9.9"}) {
		t.Fatalf("user's DNS replaced: %v", got)
	}
}

func TestOfflineNetworkIsLeftAlone(t *testing.T) {
	env, settings := tethered(), fakeSettings{}
	env.alive = map[string]bool{}
	if a := newFallback(t, env, settings).Check(); a.Done || len(settings) != 0 {
		t.Fatalf("set a fallback nothing answers: %+v %v", a, settings)
	}
}

func TestNothingRunsWhileTheTunnelHoldsTheResolver(t *testing.T) {
	env, settings := tethered(), fakeSettings{}
	env.tunnel = true
	if a := newFallback(t, env, settings).Check(); a.Done || len(settings) != 0 {
		t.Fatalf("touched the tunnel's setting: %+v %v", a, settings)
	}
}

func TestFallbackChangedBySomeoneElseIsTheirs(t *testing.T) {
	env, settings := tethered(), fakeSettings{}
	f := newFallback(t, env, settings)
	f.Check()
	settings["iPhone USB"] = []string{"1.1.1.1"}
	env.alive["en6 172.20.10.1"] = true
	f.Check()
	if got := settings["iPhone USB"]; !reflect.DeepEqual(got, []string{"1.1.1.1"}) {
		t.Fatalf("someone else's DNS handed back: %v", got)
	}
}

// The tunnel backs up our fallback as the user's setting and puts it back on
// stop; the marker still matches and the hand-back works after that.
func TestFallbackSurvivesATunnelSession(t *testing.T) {
	env, settings := tethered(), fakeSettings{}
	mu := &sync.Mutex{}
	f := newFallback(t, env, settings)
	f.mu = mu
	sys := systemDNS{dir: filepath.Join(t.TempDir(), "dns"), net: settings, mu: mu}
	f.Check()
	if err := sys.Override("172.19.0.2"); err != nil {
		t.Fatal(err)
	}
	if err := sys.Restore(); err != nil {
		t.Fatal(err)
	}
	env.alive["en6 172.20.10.1"] = true
	f.Check()
	if _, ok := settings["iPhone USB"]; ok {
		t.Fatalf("not handed back after the tunnel: %v", settings["iPhone USB"])
	}
}

// A disconnected service keeps the fallback until it is judged connected.
func TestDisconnectedServiceKeepsTheFallback(t *testing.T) {
	env, settings := tethered(), fakeSettings{}
	f := newFallback(t, env, settings)
	f.Check()
	env.primaryDev, env.dhcp = "en0", map[string][]string{"en0": {"192.168.0.1"}}
	env.alive["en0 192.168.0.1"] = true
	f.Check()
	if got := settings["iPhone USB"]; !reflect.DeepEqual(got, fallbackServers) {
		t.Fatalf("fallback dropped while unplugged: %v", got)
	}
}

func TestParseDHCPResolvers(t *testing.T) {
	out := "router (ip_mult): {172.20.10.1}\ndomain_name_server (ip_mult): {172.20.10.1, 192.168.0.1}\n"
	if got := parseDHCPResolvers(out); !reflect.DeepEqual(got, []string{"172.20.10.1", "192.168.0.1"}) {
		t.Fatalf("got %v", got)
	}
	if got := parseDHCPResolvers("router (ip_mult): {1.2.3.4}\n"); got != nil {
		t.Fatalf("got %v", got)
	}
}
