// Package voiceprobe is the one place the tester's voice probe (diag:voice) and
// the core config agree on how the probe reaches the tunnel's profile.
//
// The probe used to send its datagrams by the default route into the TUN, and
// the core's rules decided where they went. In the default mode, where only the
// blocked internet goes through the VPN, the STUN server is not blocked, so the
// series left directly and timed the tester's own network while its line said
// path=tun (17.11, tamila-001 and vitya-001: rtt 2 ms through "the tunnel").
//
// So the core carries an inbound of its own for the probe: a direct inbound on
// loopback that forwards every datagram to the STUN server, and one route rule
// sends what comes from it to the same outbound as the VPN's own traffic, in any
// routing mode. Nothing else reaches that inbound, so no application's STUN is
// moved off the path its mode gives it.
package voiceprobe

import (
	"net"
	"strconv"
)

const (
	// Tag is the inbound's tag, which the route rule matches.
	Tag = "voice-probe"
	// ListenAddress is loopback only: an open UDP forwarder on a public
	// address would serve anyone.
	ListenAddress = "127.0.0.1"
	// ListenPort is fixed, as the Clash API's 9090 is: the probe has no other
	// way to learn it. High and unassigned, so another program is unlikely to
	// hold it; the core refuses to start over a port taken.
	ListenPort = 47391
	// TargetHost and TargetPort are the STUN server the core forwards to. The
	// exit resolves the name, so the anycast answers near the exit.
	TargetHost = "stun.cloudflare.com"
	TargetPort = 3478
)

// Address is where the probe sends its datagrams.
func Address() string {
	return net.JoinHostPort(ListenAddress, strconv.Itoa(ListenPort))
}

// Inbound is the core config's inbound for the probe.
func Inbound() map[string]interface{} {
	return map[string]interface{}{
		"tag":              Tag,
		"type":             "direct",
		"listen":           ListenAddress,
		"listen_port":      ListenPort,
		"network":          "udp",
		"override_address": TargetHost,
		"override_port":    TargetPort,
	}
}
