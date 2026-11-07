package diag

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	"NekkoVPN/core/netinfo"
)

// A censor can drop some TCP flows to an address and leave others alone, decided
// by the source port, so one failed ping says nothing about how much of a path
// is left. The round below counts flows per profile, next to what the ping says,
// so a tester's log answers on its own whether a profile is cut off, thinning or
// merely slow, and which networks are on either end. A bare connect to a foreign
// :443 is unusual traffic, so this runs only in tester mode and not often.
const (
	reachEvery    = 10 * time.Minute
	reachFlows    = 12
	reachFlowWait = 3 * time.Second
	reachStagger  = 50 * time.Millisecond
	reachDelayMs  = 3500 // under clashGet's own deadline
	reachDelayURL = "http://cp.cloudflare.com/generate_204"
)

// reachControl is what the same flows should do on a path nobody cuts.
const reachControl = "1.1.1.1:443"

// reachTarget is one profile as the core's config holds it.
type reachTarget struct {
	Tag  string
	Type string
	// Proto is the type with what tells two VLESS profiles apart.
	Proto string
	Host  string
	Port  int
	UDP   bool
}

// udpOutbounds carry no TCP at all, so a TCP flow says nothing about them.
var udpOutbounds = map[string]bool{"hysteria": true, "hysteria2": true, "tuic": true, "wireguard": true}

// reachTargets lists the profiles of a sing-box config.
func reachTargets(config []byte) ([]reachTarget, error) {
	var cfg struct {
		Outbounds []struct {
			Tag        string `json:"tag"`
			Type       string `json:"type"`
			Server     string `json:"server"`
			ServerPort int    `json:"server_port"`
			QUIC       bool   `json:"quic"`
			TLS        struct {
				Reality struct {
					Enabled bool `json:"enabled"`
				} `json:"reality"`
			} `json:"tls"`
			Transport struct {
				Type string `json:"type"`
			} `json:"transport"`
		} `json:"outbounds"`
		Endpoints []struct {
			Tag  string `json:"tag"`
			Type string `json:"type"`
		} `json:"endpoints"`
	}
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, err
	}
	var out []reachTarget
	for _, o := range cfg.Outbounds {
		if strings.HasPrefix(o.Tag, "p-") {
			proto := o.Type
			if o.TLS.Reality.Enabled {
				proto += "-reality"
			}
			if o.Transport.Type != "" {
				proto += "-" + o.Transport.Type
			}
			out = append(out, reachTarget{Tag: o.Tag, Type: o.Type, Proto: proto, Host: o.Server, Port: o.ServerPort, UDP: udpOutbounds[o.Type] || (o.Type == "naive" && o.QUIC)})
		}
	}
	for _, e := range cfg.Endpoints {
		if strings.HasPrefix(e.Tag, "p-") {
			out = append(out, reachTarget{Tag: e.Tag, Type: e.Type, Proto: e.Type, UDP: true})
		}
	}
	return out, nil
}

// SetReachGate says when the reach round may run. Unset, it never does.
func (r *Runner) SetReachGate(allowed func() bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reachAllowed = allowed
}

// maybeReach runs the round when it is allowed and due.
func (r *Runner) maybeReach(ctx context.Context) {
	r.mu.Lock()
	allowed := r.reachAllowed
	due := time.Since(r.reachAt) >= reachEvery
	ifaces := r.ifaces
	r.mu.Unlock()
	if allowed == nil || !due || !allowed() {
		return
	}
	r.mu.Lock()
	r.reachAt = time.Now()
	r.mu.Unlock()
	r.reachRound(ctx, ifaces)
}

func (r *Runner) reachRound(ctx context.Context, ifaces []physIface) {
	config, err := os.ReadFile(r.configPath)
	if err != nil {
		r.log.Logf("[diag:reach] config: %v", err)
		return
	}
	targets, err := reachTargets(config)
	if err != nil {
		r.log.Logf("[diag:reach] config: %v", err)
		return
	}
	// A socket bound to the card leaves past the TUN on Windows, which the bound
	// STUN series shows. Elsewhere the address alone does not pick the route, and
	// a count taken through the tunnel would read as a healthy path.
	if runtime.GOOS != "windows" {
		ifaces = nil
	}
	for _, pi := range ifaces {
		r.log.Logf("[diag:reach] self %s isp=%s", pi.Name, netOf(publicIP(pi.IP, r.stunList())))
		through, med := countFlows(ctx, pi.IP, reachControl)
		r.log.Logf("[diag:reach] control %s flows=%d/%d med=%s", pi.Name, through, reachFlows, med)
	}
	for _, t := range targets {
		if ctx.Err() != nil {
			return
		}
		r.log.Logf("[diag:reach] %s", r.reachLine(ctx, t, ifaces))
	}
}

func (r *Runner) reachLine(ctx context.Context, t reachTarget, ifaces []physIface) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s type=%s proto=%s", t.Tag, t.Type, t.Proto)
	if ip := net.ParseIP(t.Host); ip != nil {
		fmt.Fprintf(&b, " net=%s", netOf(ip))
	}
	b.WriteString(" ping=" + pingProfile(t.Tag))
	if t.UDP || t.Port == 0 || net.ParseIP(t.Host) == nil {
		return b.String()
	}
	for _, pi := range ifaces {
		through, med := countFlows(ctx, pi.IP, net.JoinHostPort(t.Host, fmt.Sprint(t.Port)))
		fmt.Fprintf(&b, " flows[%s]=%d/%d med=%s", pi.Name, through, reachFlows, med)
	}
	return b.String()
}

// pingProfile asks the core for one request through the profile.
func pingProfile(tag string) string {
	var res struct {
		Delay int `json:"delay"`
	}
	path := fmt.Sprintf("/proxies/%s/delay?timeout=%d&url=%s", url.PathEscape(tag), reachDelayMs, url.QueryEscape(reachDelayURL))
	if err := clashGet(path, &res); err != nil {
		return fmt.Sprintf("fail(%v)", err)
	}
	return fmt.Sprintf("%dms", res.Delay)
}

// countFlows opens reachFlows connections to addr from the local address, each
// from its own source port, and counts those that opened.
func countFlows(ctx context.Context, local net.IP, addr string) (through int, median string) {
	results := make(chan time.Duration, reachFlows)
	d := net.Dialer{LocalAddr: &net.TCPAddr{IP: local}, Timeout: reachFlowWait}
	for i := 0; i < reachFlows; i++ {
		go func(wait time.Duration) {
			time.Sleep(wait)
			start := time.Now()
			conn, err := d.DialContext(ctx, "tcp4", addr)
			if err != nil {
				results <- -1
				return
			}
			took := time.Since(start)
			_ = conn.Close()
			results <- took
		}(time.Duration(i) * reachStagger)
	}
	var opened []time.Duration
	for i := 0; i < reachFlows; i++ {
		if took := <-results; took >= 0 {
			opened = append(opened, took)
		}
	}
	if len(opened) == 0 {
		return 0, "-"
	}
	sort.Slice(opened, func(i, j int) bool { return opened[i] < opened[j] })
	return len(opened), opened[len(opened)/2].Round(time.Millisecond).String()
}

// netOf names an address's network without saying the address: the log leaves
// the machine with addresses stripped, and the network is the part that matters.
func netOf(ip net.IP) string {
	if ip == nil {
		return "unknown"
	}
	info := netinfo.Lookup(ip)
	if info.ASN == 0 {
		return "unknown"
	}
	return fmt.Sprintf("AS%d", info.ASN)
}

// publicIP asks a STUN server what address the local one is seen from.
func publicIP(local net.IP, servers []string) net.IP {
	for _, addr := range servers {
		raddr, err := net.ResolveUDPAddr("udp4", addr)
		if err != nil {
			continue
		}
		conn, err := net.DialUDP("udp4", &net.UDPAddr{IP: local}, raddr)
		if err != nil {
			continue
		}
		mapped := stunMapped(conn)
		_ = conn.Close()
		if host, _, err := net.SplitHostPort(mapped); err == nil {
			if ip := net.ParseIP(host); ip != nil {
				return ip
			}
		}
	}
	return nil
}

func stunMapped(conn *net.UDPConn) string {
	req := make([]byte, 20)
	binary.BigEndian.PutUint16(req[0:], 0x0001)
	binary.BigEndian.PutUint32(req[4:], 0x2112A442)
	_, _ = rand.Read(req[8:20])
	buf := make([]byte, 1500)
	for i := 0; i < 3; i++ {
		if _, err := conn.Write(req); err != nil {
			return ""
		}
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		n, err := conn.Read(buf)
		if err == nil && n >= 20 && string(buf[8:20]) == string(req[8:20]) {
			return parseMapped(buf[:n])
		}
	}
	return ""
}
