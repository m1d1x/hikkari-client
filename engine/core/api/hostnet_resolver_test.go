package api

import (
	"testing"

	"NekkoVPN/core/netdoctor"
)

// Every config the core gets names its direct resolver where the system setting
// points at the core: also when the selection found nothing reachable, and when
// the app attached to a tunnel already running and never probed (a routing
// change rebuilds the config from that empty selection).
func TestTheCoreNeverGetsAConfigThatResolvesDirectNamesThroughItself(t *testing.T) {
	c, f := routingCore(t, false)
	for name, sel := range map[string]netdoctor.Selection{
		"unprobed":    {},
		"unreachable": {Iface: netdoctor.IfaceUnreachable, DNS: netdoctor.DNSUnchecked, Probed: true, Network: "4@192.168.1.1"},
	} {
		c.operationMu.Lock()
		c.host = sel
		err := c.rebuildConfig()
		c.operationMu.Unlock()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		f.mu.Lock()
		got := f.configuration.Host.DirectDNS
		f.mu.Unlock()
		want := ""
		if netdoctor.PinsSystemResolver {
			want = netdoctor.FallbackDNS
		}
		if got != want {
			t.Fatalf("%s: DirectDNS = %q, want %q", name, got, want)
		}
	}
}

// An unreachable selection is a moment, not the network: the next connect on
// the same network probes again instead of reusing it.
func TestAnUnreachableSelectionIsNotReusedOnTheSameNetwork(t *testing.T) {
	network := netdoctor.CurrentNetwork()
	if network == "" {
		t.Skip("no default route on this machine")
	}
	c, _ := routingCore(t, false)
	stale := netdoctor.Selection{Iface: netdoctor.IfaceUnreachable, DNS: netdoctor.DNSUnchecked, Probed: true, Network: network}
	c.operationMu.Lock()
	c.host = stale
	c.selectHostLocked()
	got := c.host
	c.operationMu.Unlock()
	// The stale selection counted no candidates; a fresh probe on a machine with
	// a default route counts at least one.
	if got.Details.Candidates == 0 {
		t.Fatalf("the unreachable selection was reused: %+v", got)
	}
}
