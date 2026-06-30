package uptime

import (
	"context"
	"fmt"
	"net"
	"os"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"

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
// the TCP fallback, and as a last resort falls back to ICMP echo so
// UDP-only services (WireGuard, Hysteria2) still show "alive" on the
// uptime graph for inactive profiles.
func (p *LiveProbe) Run(ctx context.Context, h HostEntry) (bool, int) {
	if p.Clash != nil && p.Clash.IsAlive() && h.Tag != "" {
		ms, err := p.Clash.Delay(h.Tag, "", PingTimeoutMs)
		if err != nil {
			return false, 0
		}
		return true, ms
	}
	if ok, ms := tcpProbe(ctx, h.Host, h.Port); ok {
		return true, ms
	}
	return icmpProbe(ctx, h.Host)
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

// icmpProbe sends a single ICMP echo to host and reports the RTT. Needed
// for WireGuard / Hysteria2 endpoints whose service port is UDP-only —
// tcpProbe can't shake hands with them, so the graph would otherwise
// stay flat-down for every inactive profile.
//
// Requires the process to run with privileges that allow raw ICMP. The
// Nekko engine already needs admin for TUN, so this is satisfied in
// practice; if it ever isn't, the probe just returns ok=false and the
// graph falls back to the previous (empty) behaviour.
func icmpProbe(ctx context.Context, host string) (bool, int) {
	if host == "" {
		return false, 0
	}
	dst, err := net.ResolveIPAddr("ip4", host)
	if err != nil {
		return false, 0
	}
	conn, err := icmp.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		return false, 0
	}
	defer conn.Close()

	msg := icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Code: 0,
		Body: &icmp.Echo{
			ID:   os.Getpid() & 0xffff,
			Seq:  1,
			Data: []byte("nekko"),
		},
	}
	b, err := msg.Marshal(nil)
	if err != nil {
		return false, 0
	}

	deadline := time.Now().Add(time.Duration(PingTimeoutMs) * time.Millisecond)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)

	start := time.Now()
	if _, err := conn.WriteTo(b, dst); err != nil {
		return false, 0
	}
	reply := make([]byte, 512)
	n, _, err := conn.ReadFrom(reply)
	if err != nil {
		return false, 0
	}
	parsed, err := icmp.ParseMessage(1 /* iana.ProtocolICMP */, reply[:n])
	if err != nil {
		return false, 0
	}
	if parsed.Type != ipv4.ICMPTypeEchoReply {
		return false, 0
	}
	return true, int(time.Since(start) / time.Millisecond)
}
