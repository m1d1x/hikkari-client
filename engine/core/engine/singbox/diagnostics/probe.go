package diag

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"runtime"
)

// The operator's question: does this address open from this tester's own
// network? A verdict from one prober on one hoster's route says little about a
// home line. The answer counts bare TCP connections from the physical card, past
// the tunnel, and sends nothing over them. The hub has already checked the
// targets; they are checked again here, since what this machine connects to is
// this machine's call.

const maxProbeTargets = 16

var probeLabel = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

var probeNotPublic = []netip.Prefix{netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("198.18.0.0/15")}

type probeTarget struct {
	Label string `json:"label"`
	Addr  string `json:"addr"`
}

func parseProbeTargets(params json.RawMessage) ([]probeTarget, error) {
	return parseAddrTargets(params, maxProbeTargets)
}

// parseAddrTargets reads 1 to max labelled public ipv4:port addresses, the
// targets of probe_targets and provoke_targets alike.
func parseAddrTargets(params json.RawMessage, max int) ([]probeTarget, error) {
	var req struct {
		Targets []probeTarget `json:"targets"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("params: %w", err)
	}
	if len(req.Targets) == 0 || len(req.Targets) > max {
		return nil, fmt.Errorf("want 1 to %d targets, got %d", max, len(req.Targets))
	}
	for _, t := range req.Targets {
		if !probeLabel.MatchString(t.Label) {
			return nil, fmt.Errorf("bad label")
		}
		addr, err := netip.ParseAddrPort(t.Addr)
		if err != nil || !addr.Addr().Is4() || addr.Port() == 0 {
			return nil, fmt.Errorf("target %s: want ipv4:port", t.Label)
		}
		ip := addr.Addr()
		public := ip.IsGlobalUnicast() && !ip.IsPrivate()
		for _, p := range probeNotPublic {
			public = public && !p.Contains(ip)
		}
		if !public {
			return nil, fmt.Errorf("target %s: not a public address", t.Label)
		}
	}
	return req.Targets, nil
}

// ProbeTargets answers the hub's probe_targets. Labels stand in for addresses,
// which the log strips on its way out; the network of each stays.
func ProbeTargets(ctx context.Context, params json.RawMessage) []string {
	targets, err := parseProbeTargets(params)
	if err != nil {
		return []string{fmt.Sprintf("error=%q", err.Error())}
	}
	// Only on Windows does a socket bound to the card leave past the TUN (see
	// reachRound); a count taken through the tunnel would read as a healthy path.
	if runtime.GOOS != "windows" {
		return []string{fmt.Sprintf("error=%q", "flows need Windows, this is "+runtime.GOOS)}
	}
	ifaces := physicalIfaces()
	if len(ifaces) == 0 {
		return []string{`error="no physical interface"`}
	}
	var lines []string
	for _, pi := range ifaces {
		through, med := countFlows(ctx, pi.IP, reachControl)
		lines = append(lines, fmt.Sprintf("control %s flows=%d/%d med=%s", pi.Name, through, reachFlows, med))
	}
	for _, t := range targets {
		if ctx.Err() != nil {
			return append(lines, "error=\"out of time\"")
		}
		host, _, _ := net.SplitHostPort(t.Addr)
		line := fmt.Sprintf("%s net=%s", t.Label, netOf(net.ParseIP(host)))
		for _, pi := range ifaces {
			through, med := countFlows(ctx, pi.IP, t.Addr)
			line += fmt.Sprintf(" flows[%s]=%d/%d med=%s", pi.Name, through, reachFlows, med)
		}
		lines = append(lines, line)
	}
	return lines
}
