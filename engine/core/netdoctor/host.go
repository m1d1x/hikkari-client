package netdoctor

import (
	"encoding/binary"
	"errors"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Host selection adapts the core's direct (bypass) path to the machine it
// runs on. Two things break direct traffic on a cluttered Windows install
// while the tunnel itself works:
//
//   - the interface: the core binds direct sockets to the lowest-metric
//     default route, and a foreign VPN adapter or a stale route can win it;
//   - the resolver: the adapter's DNS points at a local stub of a tool that
//     is no longer running.
//
// Both are decided by real traffic, not by adapter names or lists: the
// interface Windows itself picks is tried first, and only if a handshake
// through it fails are the others tried. Probes go to a neutral public
// resolver, never to a VPN server — a censor sees one TCP handshake to
// Yandex DNS, which every device in the country makes.
//
// Probes must run with the tunnel down: with strict_route up the system
// sends everything, DNS included, into the TUN.

// tunNet is the core's own TUN subnet (configfile.tunInbound): never a
// candidate for direct traffic, should it still be up.
var tunNet = &net.IPNet{IP: net.IPv4(172, 19, 0, 0), Mask: net.CIDRMask(30, 32)}

// ProbeTargets: Yandex DNS, reachable from anywhere in RU and harmless to
// talk to. Port 53 over TCP for the interface handshake, UDP for DNS.
var ProbeTargets = []string{"77.88.8.8", "77.88.8.1"}

// FallbackDNS replaces the system resolver for direct lookups when the
// system one is broken.
const FallbackDNS = "77.88.8.8"

// probeDomains are resolved to judge a resolver. Popular RU domains: always
// present, never blocked, answered by any working resolver.
var probeDomains = []string{"ya.ru", "vk.com", "mail.ru"}

// Interface verdicts.
const (
	IfaceAuto        = "auto"           // the interface Windows picks works: keep auto-detect
	IfacePinned      = "pinned"         // it does not; another one does and is pinned
	IfaceUnreachable = "none_reachable" // nothing reaches the probe target: offline, left as is
	IfaceUnchecked   = "unchecked"      // not probed (tunnel up, or not supported)
)

// DNS verdicts.
const (
	DNSSystem      = "system"      // the adapter's resolver answers: keep it
	DNSFallback    = "fallback"    // it does not; FallbackDNS is used for direct
	DNSUnreachable = "unreachable" // neither answers: offline, left as is
	DNSUnchecked   = "unchecked"
)

// Selection is the outcome. The zero DirectInterface / DirectDNS mean the
// core's defaults (auto-detect, system resolver).
type Selection struct {
	Iface string `json:"iface"`
	DNS   string `json:"dns"`
	// DirectInterface is the OS interface name to bind direct traffic to.
	// Not shown to the user and never logged.
	DirectInterface string `json:"-"`
	DirectDNS       string `json:"-"`
	// Network identifies the physical network the decision was made on
	// (adapters and their gateways). Opaque, never logged.
	Network string `json:"-"`
	// Probed is false when the selection is the cached one from the last
	// connect, not a fresh probe.
	Probed bool `json:"probed"`
	// Details say why the verdicts came out as they did, for support.
	// Classes and counts only: no names, no addresses.
	Details Details `json:"details"`
}

// Details is the anonymized trace of a selection.
type Details struct {
	Candidates int `json:"candidates"`
	Physical   int `json:"physical"`
	Virtual    int `json:"virtual"`
	// BestKind is the class of the interface the OS picks: physical |
	// virtual | none (no candidate is the OS's choice).
	BestKind string `json:"bestKind"`
	// BestIface names the interface the OS picks. Only for the tester log.
	BestIface string `json:"-"`
	BestOK    bool   `json:"bestOk"`
	// OthersReached counts the other candidates that completed a handshake
	// (probing stops at the first, so it is 0 or 1).
	OthersReached int `json:"othersReached"`
	OthersTried   int `json:"othersTried"`
	// ResolverKinds classifies the chosen interface's resolvers:
	// loopback | private | public, or none.
	ResolverKinds []string `json:"resolverKinds,omitempty"`
	SystemDNSOK   bool     `json:"systemDnsOk"`
	FallbackOK    bool     `json:"fallbackOk"`
}

// Default reports whether the selection leaves the core on its defaults.
func (s Selection) Default() bool { return s.DirectInterface == "" && s.DirectDNS == "" }

// LogLine is the anonymized one-line summary for the engine log.
// PinLine says what the selection did with direct lookups: an empty pin where
// the resolver has to be pinned leaves the core reading the system setting,
// which the service points at the core itself. It also names the OS's interface
// choice, which a foreign VPN changes. No addresses.
func (s Selection) PinLine() string {
	pin := "none"
	if s.DirectDNS != "" {
		pin = resolverKind(s.DirectDNS)
	}
	best := s.Details.BestIface
	if best == "" {
		best = "?"
	}
	return "pin=" + pin + " pins_required=" + strconv.FormatBool(PinsSystemResolver) + " os_iface=" + best
}

func (s Selection) LogLine() string {
	// Attached to a tunnel already running: no selection was made, and its
	// empty fields must not read as failed checks.
	if s.Iface == "" && s.DNS == "" {
		return "direct unchecked"
	}
	d := s.Details
	kinds := "none"
	if len(d.ResolverKinds) > 0 {
		kinds = strings.Join(d.ResolverKinds, "+")
	}
	return "direct iface=" + s.Iface + " dns=" + s.DNS +
		" cand=" + strconv.Itoa(d.Candidates) + "(phys=" + strconv.Itoa(d.Physical) + ",virt=" + strconv.Itoa(d.Virtual) + ")" +
		" best=" + d.BestKind + ":" + okFail(d.BestOK) +
		" others=" + strconv.Itoa(d.OthersReached) + "/" + strconv.Itoa(d.OthersTried) +
		" resolvers=" + kinds + " sys=" + okFail(d.SystemDNSOK) + " fb=" + okFail(d.FallbackOK)
}

func okFail(ok bool) string {
	if ok {
		return "ok"
	}
	return "fail"
}

// Candidate is an interface that could carry direct traffic: up, with a
// default gateway.
type Candidate struct {
	Index   int
	Name    string
	Metric  uint32
	Gateway string
	DNS     []string // IPv4 resolvers configured on it
	// Physical: an Ethernet or Wi-Fi NIC, not a virtual/tunnel adapter.
	Physical bool
}

// hostEnv is the OS side of a selection; tests stand in for it.
type hostEnv interface {
	candidates() ([]Candidate, error)
	// best is the interface the OS routes target through.
	best(target string) (int, error)
	// reach completes a TCP handshake to target:53 bound to interface idx.
	reach(idx int, target string) bool
	// resolve sends one A query for domain to server, bound to idx.
	resolve(idx int, server, domain string) bool
	sleep(time.Duration)
	// pinsResolver: see PinsSystemResolver.
	pinsResolver() bool
}

// networkKey identifies a physical network by its adapters and gateways.
func networkKey(cs []Candidate) string {
	parts := make([]string, 0, len(cs))
	for _, c := range cs {
		parts = append(parts, strconv.Itoa(c.Index)+"@"+c.Gateway)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func selectHost(env hostEnv) Selection {
	sel := Selection{Iface: IfaceUnchecked, DNS: DNSUnchecked, Probed: true}
	sel.Details.BestKind = "none"
	cs, err := env.candidates()
	if err != nil || len(cs) == 0 {
		sel.Iface, sel.DNS = IfaceUnreachable, DNSUnchecked
		return sel
	}
	sel.Network = networkKey(cs)
	sel.Details.Candidates = len(cs)
	for _, c := range cs {
		if c.Physical {
			sel.Details.Physical++
		} else {
			sel.Details.Virtual++
		}
	}

	chosen, pinned := pickInterface(env, cs, &sel.Details)
	if chosen == nil {
		sel.Iface = IfaceUnreachable
		return sel
	}
	sel.Iface = IfaceAuto
	if pinned {
		sel.Iface = IfacePinned
		sel.DirectInterface = chosen.Name
	}

	sel.DNS = judgeDNS(env, chosen, &sel.Details)
	if sel.DNS == DNSFallback {
		sel.DirectDNS = FallbackDNS
	}
	if env.pinsResolver() && sel.DirectDNS == "" {
		sel.DirectDNS = pinnedResolver(env, chosen)
	}
	return sel
}

// pickInterface returns the interface direct traffic should use and whether
// it differs from the one the OS (and so the core's auto-detect) picks.
func pickInterface(env hostEnv, cs []Candidate, d *Details) (*Candidate, bool) {
	reaches := func(c *Candidate) bool {
		for _, t := range ProbeTargets {
			if env.reach(c.Index, t) {
				return true
			}
		}
		return false
	}
	bestIdx := -1
	if idx, err := env.best(ProbeTargets[0]); err == nil {
		bestIdx = idx
		if iface, err := net.InterfaceByIndex(idx); err == nil {
			d.BestIface = iface.Name
		}
	}
	for i := range cs {
		if cs[i].Index == bestIdx {
			d.BestKind = kindOf(cs[i])
			if reaches(&cs[i]) {
				d.BestOK = true
				return &cs[i], false
			}
			break
		}
	}
	others := make([]*Candidate, 0, len(cs))
	for i := range cs {
		if cs[i].Index != bestIdx {
			others = append(others, &cs[i])
		}
	}
	sort.SliceStable(others, func(i, j int) bool { return others[i].Metric < others[j].Metric })
	for _, c := range others {
		d.OthersTried++
		if reaches(c) {
			d.OthersReached++
			// The OS's own choice was not among the candidates (no
			// gateway) or failed: the core must be told explicitly.
			return c, true
		}
	}
	return nil, false
}

func kindOf(c Candidate) string {
	if c.Physical {
		return "physical"
	}
	return "virtual"
}

// resolverKind classifies a resolver address without revealing it.
func resolverKind(ip string) string {
	a := net.ParseIP(ip)
	switch {
	case a == nil:
		return "invalid"
	case a.IsLoopback():
		return "loopback"
	case a.IsPrivate() || a.IsLinkLocalUnicast():
		return "private"
	default:
		return "public"
	}
}

// pinnedResolver names the resolver direct lookups use while the system
// setting points at the core: the first IPv4 one of the network that answers.
// Offline nothing answers, and the fallback stands in until the network change
// that follows restarts the tunnel and probes again.
func pinnedResolver(env hostEnv, c *Candidate) string {
	for _, s := range c.DNS {
		if ip := net.ParseIP(s); ip != nil && ip.To4() != nil && resolverWorks(env, c.Index, []string{s}) {
			return s
		}
	}
	return FallbackDNS
}

// judgeDNS: the system resolver is broken only if it fails twice, a short
// while apart — one dropped packet must not switch resolvers. The core's own
// address is not a resolver of this network: it shows up here when a selection
// runs under a tunnel the service already had up.
func judgeDNS(env hostEnv, c *Candidate, d *Details) string {
	real := c.DNS[:0:0]
	for _, s := range c.DNS {
		if ip := net.ParseIP(s); ip == nil || !tunNet.Contains(ip) {
			real = append(real, s)
		}
	}
	c.DNS = real
	seen := map[string]bool{}
	for _, s := range c.DNS {
		if k := resolverKind(s); !seen[k] {
			seen[k] = true
			d.ResolverKinds = append(d.ResolverKinds, k)
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			env.sleep(500 * time.Millisecond)
		}
		if resolverWorks(env, c.Index, c.DNS) {
			d.SystemDNSOK = true
			return DNSSystem
		}
	}
	if resolverWorks(env, c.Index, []string{FallbackDNS}) {
		d.FallbackOK = true
		return DNSFallback
	}
	return DNSUnreachable
}

// resolverWorks: some server answers at least two of the three domains.
func resolverWorks(env hostEnv, idx int, servers []string) bool {
	for _, s := range servers {
		ok := 0
		for _, d := range probeDomains {
			if env.resolve(idx, s, d) {
				ok++
			}
		}
		if ok >= 2 {
			return true
		}
	}
	return false
}

// buildQuery is a minimal DNS query: one A question, recursion desired.
func buildQuery(id uint16, domain string) ([]byte, error) {
	b := make([]byte, 12, 12+len(domain)+6)
	binary.BigEndian.PutUint16(b[0:], id)
	binary.BigEndian.PutUint16(b[2:], 0x0100) // RD
	binary.BigEndian.PutUint16(b[4:], 1)      // QDCOUNT
	for _, label := range strings.Split(strings.TrimSuffix(domain, "."), ".") {
		if label == "" || len(label) > 63 {
			return nil, errors.New("bad domain")
		}
		b = append(b, byte(len(label)))
		b = append(b, label...)
	}
	b = append(b, 0, 0, 1, 0, 1) // root, QTYPE A, QCLASS IN
	return b, nil
}

// answered reports whether resp is a successful answer to query id with at
// least one record.
func answered(resp []byte, id uint16) bool {
	if len(resp) < 12 {
		return false
	}
	flags := binary.BigEndian.Uint16(resp[2:])
	return binary.BigEndian.Uint16(resp[0:]) == id &&
		flags&0x8000 != 0 && // QR: a response
		flags&0x000f == 0 && // RCODE NOERROR
		binary.BigEndian.Uint16(resp[6:]) > 0 // ANCOUNT
}
