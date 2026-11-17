// Package voiceprobe is the one place the tester's voice probe (diag:voice) and
// the core config agree on how the probe's series reaches the tunnel's profile.
//
// The probe used to send its datagrams to the STUN server by the default route
// into the TUN, and the core's rules decided where they went. In the default
// mode, where only the blocked internet goes through the VPN, the STUN server
// is not blocked, so the series left directly and timed the tester's own
// network while its line said path=tun (17.11, tamila-001 and vitya-001: rtt
// 2 ms through "the tunnel").
//
// So the probe sends to an address of its own, Address, by the default route
// into the TUN, and two route rules ahead of the lists turn what goes there to
// the STUN server by name and send it to the same outbound as the VPN's own
// traffic, in any routing mode. No application sends to that address, so no
// application's STUN is moved off the path its mode gives it.
//
// Client 1.0.90 had the core listen for the probe on a fixed loopback port
// instead. Windows with Hyper-V or WSL2 reserves whole ranges of UDP ports,
// that port among them on one tester's PC, and a core that cannot bind an
// inbound does not start: the tunnel would not come up at all. A route rule
// binds nothing.
package voiceprobe

import (
	"net"
	"strconv"
)

const (
	// IP is in TEST-NET-1 (RFC 5737): documentation only, routed nowhere on
	// the internet, so nothing but the probe sends there. Not private, not
	// fake-IP: the rules ahead of the probe's (ip_is_private, the fake-IP
	// range) leave it alone, and the TUN's routes take it.
	IP = "192.0.2.1"
	// Port is the STUN port, the same as TargetPort.
	Port = 3478
	// TargetHost and TargetPort are the STUN server the core sends the series
	// to. The name goes to the exit, which resolves it, so the anycast answers
	// near the exit.
	TargetHost = "stun.cloudflare.com"
	TargetPort = 3478
)

// Address is where the probe sends its datagrams.
func Address() string {
	return net.JoinHostPort(IP, strconv.Itoa(Port))
}

// CIDR is the route rules' match for Address.
func CIDR() string { return IP + "/32" }
