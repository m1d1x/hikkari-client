package uptime

import (
	"context"
	"fmt"
	"net"
	"time"

	"NekkoVPN/core/clashapi"
)

// LiveProbe is the production Probe implementation. It tries the Clash API
// end-to-end delay first (real signal: did a request from this machine
// reach a foreign HTTP endpoint through this proxy in time); falls back to
// a TCP-connect probe when the engine isn't up (weak signal: is the host
// reachable at all).
//
// The two signals are conflated in the returned RttMs because the UI only
// has one sparkline per profile. In a future pass we could tag samples
// with their source so the UI can colour them differently, but that's a
// scope for later.
type LiveProbe struct {
	Clash *clashapi.Client
}

// NewLiveProbe wires the probe to a Clash client.
func NewLiveProbe(c *clashapi.Client) *LiveProbe { return &LiveProbe{Clash: c} }

// Run delegates to the live probe when the engine answers, otherwise to
// the TCP fallback.
func (p *LiveProbe) Run(ctx context.Context, h HostEntry) (bool, int) {
	if p.Clash != nil && p.Clash.IsAlive() && h.Tag != "" {
		ms, err := p.Clash.Delay(h.Tag, "", PingTimeoutMs)
		if err != nil {
			return false, 0
		}
		return true, ms
	}
	return tcpProbe(ctx, h.Host, h.Port)
}

// tcpProbe opens a TCP socket to host:port with a tight deadline and
// measures how long the handshake took. Closes immediately on success.
// Returns false if host or port is missing — the caller decided it has no
// way to probe this profile (e.g. WG endpoint with no port we can hit).
//
// For Hysteria2 / WireGuard servers whose service port is UDP-only, this
// fallback will always fail; that's a documented limitation, not a bug.
// The graph will show "down" until the engine comes up and the live probe
// can take over.
func tcpProbe(ctx context.Context, host string, port int) (bool, int) {
	if host == "" || port == 0 {
		return false, 0
	}
	d := net.Dialer{Timeout: time.Duration(PingTimeoutMs) * time.Millisecond}
	start := time.Now()
	conn, err := d.DialContext(ctx, "tcp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return false, 0
	}
	_ = conn.Close()
	return true, int(time.Since(start) / time.Millisecond)
}
