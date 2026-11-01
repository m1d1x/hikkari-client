package netdoctor

import (
	"encoding/binary"
	"math/rand"
	"net"
	"strings"
	"time"
)

// macOS keeps proxies and manual resolvers per network service and for the
// whole machine, not per user: reading them takes no rights, changing them
// takes root. So there the doctor's repairs run in the service, and the same
// conservative rule as on Windows applies: only what is provably orphaned is
// changed, a live local proxy or a remote one is the user's choice.

// ServiceProxy is one proxy kind (web, secure, socks) of one network service.
type ServiceProxy struct {
	Service string
	Kind    string
	Setting ProxySetting
}

// proxyStore is the OS side of the per-service proxies; tests stand in for it.
type proxyStore interface {
	list() ([]ServiceProxy, error)
	disable(service, kind string) error
}

// parseServiceProxy reads what `networksetup -getwebproxy` and its siblings
// print: "Enabled: Yes", "Server: 127.0.0.1", "Port: 7890". The labels are not
// localized: networksetup prints English in every system language.
func parseServiceProxy(out string) ProxySetting {
	var s ProxySetting
	var host, port string
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "Enabled":
			s.Enabled = strings.EqualFold(v, "Yes")
		case "Server":
			host = v
		case "Port":
			port = v
		}
	}
	if host != "" {
		s.Server = net.JoinHostPort(host, port)
	}
	return s
}

// classifyByService judges each network service as a whole: a tool usually
// sets several kinds at once, and one live endpoint means it still runs. A
// service's verdict is what Classify gives all its enabled endpoints. The
// result is the most actionable verdict among the services, and the services
// that verdict is orphan for.
func classifyByService(list []ServiceProxy) (ProxyReport, map[string]bool) {
	var order []string
	servers := map[string][]string{}
	for _, p := range list {
		if !p.Setting.Enabled || p.Setting.Server == "" {
			continue
		}
		if _, seen := servers[p.Service]; !seen {
			order = append(order, p.Service)
		}
		servers[p.Service] = append(servers[p.Service], p.Setting.Server)
	}
	rank := map[string]int{ProxyNone: 0, ProxyRemote: 1, ProxyLive: 2, ProxyOrphan: 3}
	report := ProxyReport{Verdict: ProxyNone}
	orphans := map[string]bool{}
	for _, service := range order {
		r := Classify(ProxySetting{Enabled: true, Server: strings.Join(servers[service], ";")})
		if r.Verdict == ProxyOrphan {
			orphans[service] = true
		}
		if rank[r.Verdict] > rank[report.Verdict] {
			report = r
		}
	}
	return report, orphans
}

// sameProxies compares two readings of the store for the kinds of the given
// services.
func sameProxies(a, b []ServiceProxy, services map[string]bool) bool {
	pick := func(list []ServiceProxy) map[[2]string]ProxySetting {
		m := map[[2]string]ProxySetting{}
		for _, p := range list {
			if services[p.Service] {
				m[[2]string{p.Service, p.Kind}] = p.Setting
			}
		}
		return m
	}
	x, y := pick(a), pick(b)
	if len(x) != len(y) {
		return false
	}
	for k, v := range x {
		if y[k] != v {
			return false
		}
	}
	return true
}

// repairProxies switches off the enabled kinds of the orphaned services. The
// host and port stay, so the user can turn the same proxy back on. The store
// is read again right before writing: if the setting changed since the probe,
// the verdict no longer applies.
func repairProxies(store proxyStore) (Action, error) {
	a := Action{Name: "proxy_disable"}
	list, err := store.list()
	if err != nil {
		return a, err
	}
	report, orphans := classifyByService(list)
	switch report.Verdict {
	case ProxyLive:
		a.Detail = "system proxy is in use, left as is"
		return a, nil
	case ProxyRemote:
		a.Detail = "system proxy is not local, left as is"
		return a, nil
	case ProxyNone:
		a.Detail = "no system proxy"
		return a, nil
	}
	now, err := store.list()
	if err != nil {
		a.Detail = err.Error()
		return a, nil
	}
	if !sameProxies(list, now, orphans) {
		a.Detail = "proxy setting changed during repair, left as is"
		return a, nil
	}
	var failed []string
	for _, p := range list {
		if !orphans[p.Service] || !p.Setting.Enabled {
			continue
		}
		if err := store.disable(p.Service, p.Kind); err != nil {
			failed = append(failed, err.Error())
		}
	}
	if len(failed) > 0 {
		a.Detail = strings.Join(failed, "; ")
		return a, nil
	}
	a.Done = true
	a.Detail = "disabled orphan system proxy"
	return a, nil
}

// deadLoopbackDNS reports whether manually set resolvers are the leftover of
// a local DNS stub: all of them on this machine and none answering. A remote
// or a live resolver is the user's choice, and no manual setting at all means
// DHCP, which is nothing to repair.
func deadLoopbackDNS(servers []string, alive func(string) bool) bool {
	if len(servers) == 0 {
		return false
	}
	for _, s := range servers {
		ip := net.ParseIP(s)
		if ip == nil || !ip.IsLoopback() {
			return false
		}
	}
	for _, s := range servers {
		if alive(s) {
			return false
		}
	}
	return true
}

// dnsStubAlive reports whether anything answers DNS at server: any reply to
// our query counts, even an error — a stub whose upstream is down still runs.
func dnsStubAlive(server string) bool {
	id := uint16(rand.Uint32())
	q, err := buildQuery(id, probeDomains[0])
	if err != nil {
		return false
	}
	conn, err := net.DialTimeout("udp", net.JoinHostPort(server, "53"), dialTimeout)
	if err != nil {
		return false
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	if _, err := conn.Write(q); err != nil {
		return false
	}
	resp := make([]byte, 1500)
	for {
		n, err := conn.Read(resp)
		if err != nil {
			return false
		}
		if n >= 2 && binary.BigEndian.Uint16(resp) == id {
			return true
		}
	}
}

// parseResolvers reads `scutil --dns`: the default resolvers, and the ones
// scoped to an interface, by its index. Resolvers for one domain only (mDNS,
// /etc/resolver) answer for that domain and are not what direct traffic uses.
func parseResolvers(out string) (global []string, scoped map[int][]string) {
	scoped = map[int][]string{}
	inScoped := false
	var servers []string
	ifIndex := 0
	domainOnly := false
	flush := func() {
		if len(servers) > 0 && !domainOnly {
			switch {
			case inScoped && ifIndex > 0:
				scoped[ifIndex] = append(scoped[ifIndex], servers...)
			case !inScoped && global == nil:
				global = servers
			}
		}
		servers, ifIndex, domainOnly = nil, 0, false
	}
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "DNS configuration"):
			flush()
			inScoped = strings.Contains(line, "scoped")
		case strings.HasPrefix(line, "resolver #"):
			flush()
		case strings.HasPrefix(line, "nameserver["):
			if _, v, ok := strings.Cut(line, ":"); ok {
				// IPv4 only: the probes and the candidates are IPv4.
				if ip := net.ParseIP(strings.TrimSpace(v)); ip != nil && ip.To4() != nil {
					servers = append(servers, ip.String())
				}
			}
		case strings.HasPrefix(line, "if_index"):
			if _, v, ok := strings.Cut(line, ":"); ok {
				fields := strings.Fields(v)
				if len(fields) > 0 {
					ifIndex = atoiOrZero(fields[0])
				}
			}
		case strings.HasPrefix(line, "domain"):
			// "search domain[0]" does not start with it, on purpose.
			domainOnly = true
		}
	}
	flush()
	return global, scoped
}

func atoiOrZero(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}
