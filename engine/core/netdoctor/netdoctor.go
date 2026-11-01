// Package netdoctor diagnoses and repairs host network settings that third-
// party VPNs and proxies leave behind and that break hikkari's direct
// (bypass) traffic: a WinINet system proxy pointing at a dead local port,
// and a poisoned DNS resolver cache.
//
// It runs in the GUI process, as the signed-in user: the WinINet proxy lives
// in HKCU, so the SYSTEM service would see and repair the wrong user's hive.
//
// Repair is deliberately conservative. A proxy is switched off only when it
// is provably orphaned — it points at a loopback address and nothing listens
// there. A live local proxy (a running Clash) or a remote one (a corporate
// proxy) is the user's choice and is reported, never touched.
package netdoctor

import (
	"net"
	"strconv"
	"strings"
	"time"
)

// ProxySetting is the WinINet "Internet Settings" proxy state of the user.
type ProxySetting struct {
	Enabled       bool   `json:"enabled"`
	Server        string `json:"server,omitempty"`
	AutoConfigURL string `json:"autoConfigUrl,omitempty"`
}

// Proxy verdicts.
const (
	ProxyNone   = "none"   // no proxy configured
	ProxyLive   = "live"   // a local proxy that is listening: leave it
	ProxyRemote = "remote" // a non-loopback proxy (corporate): leave it
	ProxyOrphan = "orphan" // loopback proxy, nothing listening: safe to disable
)

// ProxyReport is the proxy part of a diagnosis.
type ProxyReport struct {
	Setting ProxySetting `json:"setting"`
	Verdict string       `json:"verdict"`
	// Endpoints are the host:port pairs Server names, in order.
	Endpoints []string `json:"endpoints,omitempty"`
}

// Report is a whole diagnosis.
type Report struct {
	// Supported is false on platforms the doctor has no checks for; the
	// rest of the report is then empty.
	Supported bool        `json:"supported"`
	Proxy     ProxyReport `json:"proxy"`
	// OS is runtime.GOOS: which checks and wording apply.
	OS string `json:"os"`
}

// RepairResult says what Repair actually changed. Every action is reported
// with its outcome, so the UI log shows exactly what happened to the system.
type RepairResult struct {
	Before  Report   `json:"before"`
	Actions []Action `json:"actions"`
}

// Action is one repair step.
type Action struct {
	Name string `json:"name"` // proxy_disable | dns_reset | dns_flush | reconnect
	// Done is false when the step was skipped (nothing to fix, or not ours
	// to fix) or failed; Detail says which.
	Done   bool   `json:"done"`
	Detail string `json:"detail,omitempty"`
}

// dialTimeout bounds the liveness probe of one local proxy endpoint. A
// listener on loopback answers in microseconds; a dead port refuses at once.
const dialTimeout = 400 * time.Millisecond

// probe reports whether something accepts connections at hostport.
// A variable so tests can stand in for the network.
var probe = func(hostport string) bool {
	conn, err := net.DialTimeout("tcp", hostport, dialTimeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// ParseProxyServer splits a WinINet ProxyServer value into host:port
// endpoints. It accepts both forms Windows writes: a single "host:port" for
// all protocols, and per-protocol "http=h:p;https=h:p;socks=h:p". A missing
// port gets WinINet's default of 80. Duplicates are dropped.
func ParseProxyServer(v string) []string {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.FieldsFunc(v, func(r rune) bool { return r == ';' || r == ' ' }) {
		if i := strings.IndexByte(part, '='); i >= 0 {
			part = part[i+1:]
		}
		part = strings.TrimPrefix(strings.TrimPrefix(part, "http://"), "socks://")
		part = strings.TrimSuffix(part, "/")
		if part == "" {
			continue
		}
		if _, _, err := net.SplitHostPort(part); err != nil {
			part = net.JoinHostPort(strings.Trim(part, "[]"), "80")
		}
		if !seen[part] {
			seen[part] = true
			out = append(out, part)
		}
	}
	return out
}

// isLoopbackEndpoint reports whether hostport points at this machine.
func isLoopbackEndpoint(hostport string) bool {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		return false
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Classify gives a proxy setting its verdict. Only a setting whose every
// endpoint is loopback and dead is an orphan: one live endpoint means some
// program still serves it, one remote endpoint means it is not a leftover
// of a local tool. A PAC URL alone is never judged here.
func Classify(s ProxySetting) ProxyReport {
	r := ProxyReport{Setting: s, Verdict: ProxyNone}
	if !s.Enabled {
		return r
	}
	r.Endpoints = ParseProxyServer(s.Server)
	if len(r.Endpoints) == 0 {
		return r
	}
	for _, ep := range r.Endpoints {
		if !isLoopbackEndpoint(ep) {
			r.Verdict = ProxyRemote
			return r
		}
	}
	for _, ep := range r.Endpoints {
		if probe(ep) {
			r.Verdict = ProxyLive
			return r
		}
	}
	r.Verdict = ProxyOrphan
	return r
}
