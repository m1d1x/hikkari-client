package uptime

import (
	"context"
	"testing"
)

// The probe runs in the unprivileged app: a UDP-only profile's only liveness
// signal must not need root.
func TestICMPProbeReachesLoopbackWithoutRoot(t *testing.T) {
	p := NewLiveProbe(nil)
	if _, err := p.icmpConnOrInit(); err != nil {
		t.Skipf("this host allows no ICMP socket: %v", err)
	}
	if ok, _ := p.icmpProbe(context.Background(), "127.0.0.1"); !ok {
		t.Fatal("no echo reply from loopback")
	}
}
