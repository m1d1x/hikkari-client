package uptime

import (
	"context"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
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

	// icmpMu guards lazy init / re-init of icmpConn. icmpConn is a
	// long-lived raw socket reused across probes — opening one per call
	// leaks Windows handles under load (we saw inactive-profile graphs
	// die after ~1h of background runtime), and is slow anyway.
	icmpMu   sync.Mutex
	icmpConn *icmp.PacketConn
	icmpSeq  uint32
}

// NewLiveProbe wires the probe to a Clash client.
func NewLiveProbe(c *clashapi.Client) *LiveProbe { return &LiveProbe{Clash: c} }

// Run delegates to the live probe when the engine answers, otherwise to
// the TCP fallback, and as a last resort falls back to ICMP echo so
// UDP-only services (WireGuard, Hysteria2) still show "alive" on the
// uptime graph for inactive profiles.
func (p *LiveProbe) Run(ctx context.Context, h HostEntry) (bool, int) {
	if p.Clash != nil && p.Clash.IsAlive() && h.Tag != "" {
		// Once the engine is up, every dial from this process is
		// captured by sing-box's TUN. TCP/ICMP fallbacks then measure
		// how fast sing-box ACKs the local SYN — not whether the remote
		// is alive — and consistently report ~2ms even for dead hosts.
		// Trust only the Clash signal here; an error means the tunnel
		// to that profile isn't usable, so we honestly report "down".
		ms, err := p.Clash.Delay(h.Tag, "", PingTimeoutMs)
		if err != nil {
			return false, 0
		}
		return true, ms
	}
	if ok, ms := tcpProbe(ctx, h.Host, h.Port); ok {
		return true, ms
	}
	return p.icmpProbe(ctx, h.Host)
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
// icmpConnOrInit returns a usable raw ICMP socket, opening one on first
// use. If a previous socket is dead (network suspend/resume, driver hiccup,
// Windows interface change) it's recreated on the next call. The socket
// lives for the lifetime of the LiveProbe — opening one per probe leaks
// kernel handles on Windows and was the root cause of the inactive-profile
// graphs dying after long alt-tab sessions.
func (p *LiveProbe) icmpConnOrInit() (*icmp.PacketConn, error) {
	p.icmpMu.Lock()
	defer p.icmpMu.Unlock()
	if p.icmpConn != nil {
		return p.icmpConn, nil
	}
	c, err := icmp.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		return nil, err
	}
	p.icmpConn = c
	return c, nil
}

func (p *LiveProbe) dropICMP() {
	p.icmpMu.Lock()
	defer p.icmpMu.Unlock()
	if p.icmpConn != nil {
		_ = p.icmpConn.Close()
		p.icmpConn = nil
	}
}

func (p *LiveProbe) icmpProbe(ctx context.Context, host string) (bool, int) {
	if host == "" {
		return false, 0
	}
	dst, err := net.ResolveIPAddr("ip4", host)
	if err != nil {
		return false, 0
	}
	conn, err := p.icmpConnOrInit()
	if err != nil {
		return false, 0
	}

	seq := int(atomic.AddUint32(&p.icmpSeq, 1) & 0xffff)
	msg := icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Code: 0,
		Body: &icmp.Echo{
			ID:   os.Getpid() & 0xffff,
			Seq:  seq,
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
	if err := conn.SetDeadline(deadline); err != nil {
		p.dropICMP()
		return false, 0
	}

	start := time.Now()
	if _, err := conn.WriteTo(b, dst); err != nil {
		p.dropICMP()
		return false, 0
	}
	// Reads on a shared socket may pick up replies destined for sibling
	// probes; loop until we see our seq or hit the deadline.
	reply := make([]byte, 512)
	for {
		n, _, err := conn.ReadFrom(reply)
		if err != nil {
			// Timeout is expected when host is unreachable. Don't drop
			// the socket on timeout — keep it warm for the next probe.
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				return false, 0
			}
			p.dropICMP()
			return false, 0
		}
		parsed, err := icmp.ParseMessage(1 /* iana.ProtocolICMP */, reply[:n])
		if err != nil {
			continue
		}
		if parsed.Type != ipv4.ICMPTypeEchoReply {
			continue
		}
		echo, ok := parsed.Body.(*icmp.Echo)
		if !ok || echo.Seq != seq {
			continue
		}
		return true, int(time.Since(start) / time.Millisecond)
	}
}
