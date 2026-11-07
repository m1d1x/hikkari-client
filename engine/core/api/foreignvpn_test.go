package api

import (
	"slices"
	"testing"

	"NekkoVPN/core/engine"
	"NekkoVPN/core/netdoctor"
)

// A connect ends the other client's tunnel first, says so to the UI, and
// probes the direct path afresh: the cached one ran under that tunnel.
func TestConnectStopsForeignVPNAndReselectsHost(t *testing.T) {
	f := &alternateEngine{caps: engine.Capabilities{TUN: true, DomainRouting: true, IPRouting: true, AutoSelect: true, LiveProfileSwitch: true, ProfileProbe: true, ProcessRouting: true, FolderRouting: true}}
	c := initAlternate(t, f)
	if _, err := c.AddProfileFromURI("p-first"); err != nil {
		t.Fatal(err)
	}
	asked := 0
	var hostAtStop netdoctor.Selection
	c.stopForeignVPN = func() []string {
		asked++
		hostAtStop = c.host
		return []string{"AmneziaVPN"}
	}
	stale := netdoctor.Selection{Probed: true, Network: "stale-network"}
	c.host = stale

	data, err := c.dispatch("StartVPN", "")
	if err != nil {
		t.Fatal(err)
	}
	outcome, ok := data.(ConnectOutcome)
	if !ok || !slices.Equal(outcome.ForeignVPNStopped, []string{"AmneziaVPN"}) {
		t.Fatalf("outcome %#v", data)
	}
	if asked != 1 || hostAtStop.Network != stale.Network {
		t.Fatalf("asked %d times, host before the stop %+v", asked, hostAtStop)
	}
	if c.host.Network == stale.Network {
		t.Fatal("the selection made under the other tunnel was reused")
	}

	// Already up: nothing to stop, nothing asked.
	if _, err := c.dispatch("StartVPN", ""); err != nil {
		t.Fatal(err)
	}
	if asked != 1 {
		t.Fatalf("asked %d times", asked)
	}
}

// An injected engine leaves other clients alone.
func TestInjectedEngineDoesNotStopForeignVPN(t *testing.T) {
	c := initAlternate(t, &alternateEngine{caps: engine.Capabilities{TUN: true, DomainRouting: true, IPRouting: true, AutoSelect: true, LiveProfileSwitch: true, ProfileProbe: true, ProcessRouting: true, FolderRouting: true}})
	if c.stopForeignVPN != nil {
		t.Fatal("an injected engine got the service's foreign VPN stop")
	}
}
