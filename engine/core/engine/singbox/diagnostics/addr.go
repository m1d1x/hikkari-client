package diag

import (
	"context"
	"crypto/tls"
	"net"
	"strconv"
	"sync/atomic"
	"time"
)

// A banned address opens TCP and then loses the first packet of data, whatever
// the protocol (NTC Party 26076): the profile reads red whichever fingerprint or
// SNI it has, and a reader of the line would blame the setting. So a load round
// first sends a ClientHello on addrFlows flows and waits for a byte back. A
// censor drops some flows by source port and leaves others
// (plans/2026-11-02-tcp-flow-loss.md), so one answer on any flow clears the
// address; only silence on all of them is a dead address.
const (
	addrFlows = reachFlows
	addrWait  = 8 * time.Second
)

// addrDead says the profile's address did not answer on any flow, and how many
// of them opened. It never finds a dead address it could not have asked: a
// profile with no TLS would not answer a ClientHello alive or not, a UDP one has
// no TCP to ask, and a name that does not resolve here says nothing about the
// address (the core's own lines will say what it finds).
func addrDead(ctx context.Context, t loadTarget, local net.IP) (tcp int, dead bool) {
	if local == nil {
		return 0, false
	}
	kind, _ := t.Outbound["type"].(string)
	tlsCfg, _ := t.Outbound["tls"].(map[string]interface{})
	on, _ := tlsCfg["enabled"].(bool)
	if udpOutbounds[kind] || !on {
		return 0, false
	}
	host, _ := t.Outbound["server"].(string)
	port := outboundPort(t.Outbound)
	if host == "" || port == 0 {
		return 0, false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
		if err != nil || len(ips) == 0 {
			return 0, false
		}
		ip = ips[0]
	}
	sni, _ := tlsCfg["server_name"].(string)
	opened, answered := helloFlows(ctx, local, net.JoinHostPort(ip.String(), strconv.Itoa(port)), sni, addrWait)
	return opened, answered == 0 && ctx.Err() == nil
}

func outboundPort(ob map[string]interface{}) int {
	switch p := ob["server_port"].(type) {
	case float64:
		return int(p)
	case int:
		return p
	}
	return 0
}

// helloFlows sends a ClientHello on addrFlows connections, each from its own
// source port, and counts those that opened and those that got an answer.
func helloFlows(ctx context.Context, local net.IP, addr, sni string, wait time.Duration) (opened, answered int) {
	type result struct{ opened, answered bool }
	results := make(chan result, addrFlows)
	d := net.Dialer{LocalAddr: &net.TCPAddr{IP: local}, Timeout: wait}
	for i := 0; i < addrFlows; i++ {
		go func(stagger time.Duration) {
			time.Sleep(stagger)
			conn, err := d.DialContext(ctx, "tcp4", addr)
			if err != nil {
				results <- result{}
				return
			}
			defer conn.Close()
			results <- result{opened: true, answered: helloAnswered(conn, sni, wait)}
		}(time.Duration(i) * reachStagger)
	}
	for i := 0; i < addrFlows; i++ {
		r := <-results
		if r.opened {
			opened++
		}
		if r.answered {
			answered++
		}
	}
	return opened, answered
}

// helloAnswered is whether anything came back for a ClientHello: a byte, or the
// connection refused outright. A reset is not the silence of a banned address,
// and a server may well dislike this hello (Reality passes it to its cover, a
// fingerprint check may not): only running out of time counts as silence.
func helloAnswered(conn net.Conn, sni string, wait time.Duration) bool {
	_ = conn.SetDeadline(time.Now().Add(wait))
	seen := &readCounter{Conn: conn}
	// Only asks whether the address answers: no data follows, so whose
	// certificate comes back does not matter.
	err := tls.Client(seen, &tls.Config{ServerName: sni, InsecureSkipVerify: true}).Handshake() //nolint:gosec
	return seen.n.Load() > 0 || err == nil || !isTimeout(err)
}

// readCounter counts the bytes read from a connection.
type readCounter struct {
	net.Conn
	n atomic.Int64
}

func (c *readCounter) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	c.n.Add(int64(n))
	return n, err
}
