//go:build !windows

package uptime

import (
	"net"
	"time"
)

// pingHost on non-Windows is a TCP-probe fallback: we measure how long it
// takes to open (and immediately close) a connection to host:443. It's not
// ICMP, but for the uptime widget it answers the same question — "can we
// reach this server right now" — without needing CAP_NET_RAW or root.
func pingHost(host string) (bool, int) {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, "443"),
		time.Duration(PingTimeoutMs)*time.Millisecond)
	if err != nil {
		return false, 0
	}
	_ = conn.Close()
	return true, int(time.Since(start) / time.Millisecond)
}
