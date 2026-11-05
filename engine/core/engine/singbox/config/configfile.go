// Package configfile reads/writes the sing-box config.json and the
// "whitelist" (direct-bypass) route rules the UI exposes.
//
// The on-disk format is JSONC — we strip whole-line // comments before
// json.Unmarshal so the user can keep notes inline.
package configfile

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"NekkoVPN/core/ruzone"
)

// Store is a thin file-backed config repository. It owns two files:
// config.json (the sing-box config) and routing.json (the routing state
// the UI mutates). They sit in the same directory.
type Store struct {
	path        string
	routingPath string
}

func New(path string) *Store {
	return &Store{
		path:        path,
		routingPath: filepath.Join(filepath.Dir(path), "routing.json"),
	}
}

func (s *Store) Path() string { return s.path }

// AutoTunnelPath is the rule set of hosts core/autotunnel maintains.
func (s *Store) AutoTunnelPath() string {
	return filepath.Join(filepath.Dir(s.path), "auto-tunnel.json")
}
func (s *Store) RoutingPath() string { return s.routingPath }

// Read loads config.json. Like ReadRouting, a missing file yields the
// default rather than an error — on a fresh install nothing has seeded one
// yet, and RebuildActive must still be able to compile the user's first
// profile into a working config.
func (s *Store) Read() (map[string]interface{}, error) {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return DefaultConfig(), nil
	}
	if err != nil {
		return nil, err
	}
	clean := stripLineComments(raw)
	var cfg map[string]interface{}
	if err := json.Unmarshal(clean, &cfg); err != nil {
		return nil, fmt.Errorf("config.json parse: %w", err)
	}
	return cfg, nil
}

func (s *Store) Write(cfg map[string]interface{}) error {
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	return os.WriteFile(s.path, out, 0644)
}

// DefaultConfig is the skeleton sing-box config a fresh install starts from:
// everything the engine does NOT manage itself (log, dns, inbounds, the
// Clash API binding, the sniff/hijack-dns prelude). Outbounds, endpoints,
// route.rules and route.final are left to RebuildActive/ApplyRouting, which
// overwrite them from profiles.json + routing.json on every change.
// singboxLogBlock is the log configuration handed to the core.
//
// level is "warn", not "info". At info the core narrates every connection,
// every resolved process path and every tun packet, and all of it is piped
// into the app's own log file: a four-minute session produced 414 KB, of
// which 338 KB was this narration — about 100 KB a minute, which rotates the
// 8 MB log away in under two hours and takes the app's own startup and health
// lines with it. Per-connection detail for diagnostics comes from core/diag
// (Clash API /connections), and the patched xhttp transport reports slow
// uploads at warn level, so nothing needed is lost.
//
// No "disable_color": sing-box 1.14 rejects that field and refuses to start.
// ANSI colour codes are stripped in logging.PipeLines instead.
func singboxLogBlock() map[string]interface{} {
	return map[string]interface{}{
		"level":     "warn",
		"timestamp": true,
	}
}

func tunInbound() map[string]interface{} {
	stack := "gvisor"
	if runtime.GOOS == "windows" {
		stack = "mixed"
	}
	in := map[string]interface{}{
		"tag":                      "tun-in",
		"type":                     "tun",
		"address":                  []interface{}{"172.19.0.1/30", "fdfe:dcba:9876::1/126"},
		"auto_route":               true,
		"stack":                    stack,
		"mtu":                      1360,
		"endpoint_independent_nat": true,
	}
	// Windows: without strict_route, WebRTC apps (Discord voice) bind UDP
	// sockets to the physical NIC's IP and Windows sends those packets out
	// that NIC, bypassing the TUN entirely. strict_route adds WFP rules that
	// force everything through tun-in (Clash Verge ships the same default).
	if runtime.GOOS == "linux" || runtime.GOOS == "windows" {
		in["strict_route"] = true
	}
	return in
}

// canonicalDNS is the one DNS block the engine runs with. Every A/AAAA query
// from the TUN gets a fake address and the name travels to the exit server, so
// proxied names never touch a resolver here. dns-direct answers only what must
// not be faked (realAddressRule) and the core's own lookups; dns.final is not
// reached by TUN traffic, which is why it needs no tunnelled resolver.
func canonicalDNS() map[string]interface{} {
	return map[string]interface{}{
		"servers": []interface{}{
			map[string]interface{}{
				"tag":         "fakeip-dns",
				"type":        "fakeip",
				"inet4_range": "198.18.0.0/15",
				// A v6 fake address only for a name with no A record (patch
				// 0004): a name with an A record keeps the IPv4-only answer
				// that Discord voice needs, while an IPv6-only site such as
				// ntc.party still resolves. dns-direct runs the A probe.
				"inet6_range":              "fc00::/18",
				"inet6_only_without_a_via": "dns-direct",
			},
			map[string]interface{}{
				"tag":  "dns-direct",
				"type": "local",
			},
		},
		"rules": []interface{}{
			map[string]interface{}{
				"domain_suffix": []interface{}{"zovfy.ru"},
				"server":        "dns-direct",
			},
			// fakeip only answers A/AAAA; anything else (HTTPS, SVCB,
			// PTR…) used to come back as an error the OS kept retrying
			// every second. An empty NOERROR ends the retry loop. Only the
			// query type is inverted: an invert over the whole rule also
			// emptied the core's own lookups, which have no inbound, and a
			// WireGuard profile's delay test found no address (tester-002,
			// 04.11: every probe of the Amnezia profile said 503).
			map[string]interface{}{
				"type": "logical",
				"mode": "and",
				"rules": []interface{}{
					map[string]interface{}{"inbound": []interface{}{"tun-in"}},
					map[string]interface{}{"query_type": []interface{}{"A", "AAAA"}, "invert": true},
				},
				"action": "predefined",
				"rcode":  "NOERROR",
			},
			realAddressRule(),
			map[string]interface{}{
				"inbound": []interface{}{"tun-in"},
				"server":  "fakeip-dns",
			},
		},
		"final":           "dns-direct",
		"strategy":        "prefer_ipv4",
		"reverse_mapping": true,
	}
}

// realAddressRule lists the names whose answer is itself the signal, so a
// 198.18.x.x reply breaks them: Windows NCSI compares dns.msftncsi.com against
// a fixed address and reports "no internet" otherwise, WebRTC peers exchange
// the STUN address they resolved, NTP and captive-portal clients start before
// any tunnel is usable, and LAN names exist only on the local resolver. Only
// the DNS answer changes here; routing still decides where the connection goes.
func realAddressRule() map[string]interface{} {
	return map[string]interface{}{
		"domain": []interface{}{
			"captive.apple.com",
			"connectivitycheck.android.com",
			"connectivitycheck.gstatic.com",
			"detectportal.firefox.com",
		},
		"domain_suffix": []interface{}{
			"msftconnecttest.com", "msftncsi.com",
			"time.windows.com", "time.apple.com", "time.google.com", "time.cloudflare.com", "ntp.org",
			"lan", "local", "internal", "home.arpa", "localhost",
		},
		"domain_regex": []interface{}{
			`^time[0-9]*\.[^.]+\.com$`,
			`(^|\.)stun[0-9]*\.`,
		},
		"server": "dns-direct",
	}
}

func DefaultConfig() map[string]interface{} {
	return map[string]interface{}{
		"log": singboxLogBlock(),
		"dns": canonicalDNS(),
		"inbounds": []interface{}{
			tunInbound(),
		},
		"outbounds": []interface{}{
			map[string]interface{}{"tag": "direct", "type": "direct"},
		},
		"route": map[string]interface{}{
			// Kept verbatim by ApplyRouting's prelude check.
			"rules": append(realIPv6Rules(),
				map[string]interface{}{
					"action": "sniff",
					"port":   []interface{}{80, 443, 8080, 8443},
				},
				map[string]interface{}{"protocol": "dns", "action": "hijack-dns"},
			),
			"final":                 SelectorTag,
			"auto_detect_interface": true,
			// Attaches process name/path to every connection (sing-box log
			// lines and Clash API /connections) — core/diag relies on it.
			"find_process": true,
			// Names in outbound configs (a profile's "server", say) resolve
			// through this server, outside the tunnel. Mandatory since
			// sing-box 1.14 — a missing one is a hard error.
			"default_domain_resolver": map[string]interface{}{
				"server": "dns-direct",
			},
		},
		"experimental": map[string]interface{}{
			// clashapi.DefaultBaseURL is hardcoded against this binding.
			"clash_api": map[string]interface{}{
				"external_controller": "127.0.0.1:9090",
			},
			"cache_file": map[string]interface{}{
				"enabled":      true,
				"path":         "cache.db",
				"store_fakeip": true,
			},
		},
	}
}

// ProfileSpec is the slice of profile data RebuildActive needs. Defined
// here (rather than imported from core/profile) to keep configfile free of
// upstream-package dependencies — the api layer marshals this on the way
// down.
type ProfileSpec struct {
	Tag    string
	Kind   string
	Config map[string]interface{}
}

const (
	// SelectorTag is the sing-box `selector` outbound that wraps every
	// profile. route.final and dns.detour point at this; switching
	// profiles is `PUT /proxies/proxy {name: <tag>}` to the Clash API.
	SelectorTag = "proxy"

	// AutoTag is the optional `urltest` outbound that wraps the selector.
	// When auto-fastest mode is on we make route.final point here and
	// sing-box auto-picks the lowest-latency live profile every interval.
	AutoTag = "auto"

	kindEndpoint = "endpoint"
	kindOutbound = "outbound"
)

// systemOutboundTags are the outbounds we leave alone when rebuilding —
// anything else is treated as profile-owned and gets replaced.
var systemOutboundTags = map[string]bool{
	"direct":  true,
	"block":   true,
	"dns":     true,
	"dns-out": true,
}

// managedGroupTags are the outbound tags we own and unconditionally
// rebuild every time — selector + urltest. Anything tagged with these in
// the existing outbounds list gets dropped on the way through.
var managedGroupTags = map[string]bool{
	SelectorTag: true,
	AutoTag:     true,
}

// RebuildActive loads every known profile into config.json under a single
// `selector` group named SelectorTag, defaults it to activeTag, and points
// route.final / dns.detour at SelectorTag. Preserves user-managed bits:
// inbounds, log, experimental, dns.servers/rules, and existing route.rules
// (with one exception: any "direct" rule the user set survives; only the
// auto-inserted ICMP rule is managed by us).
//
// migrateLegacyTags is true on first run after the selector model lands —
// any route.final / dns.detour still pointing at "awg-ep" or "proxy-out"
// gets rewritten to SelectorTag. After migration, this should be false.
func (s *Store) RebuildActive(specs []ProfileSpec, activeTag string, autoMode bool, legacyTags map[string]bool) error {
	state, err := s.ReadRouting()
	if err != nil {
		return err
	}
	return s.RebuildActiveWithRouting(specs, activeTag, autoMode, legacyTags, state, HostNetwork{})
}
func (s *Store) RebuildActiveWithRouting(specs []ProfileSpec, activeTag string, autoMode bool, legacyTags map[string]bool, routing RoutingState, host HostNetwork) error {
	cfg, err := s.Read()
	if err != nil {
		return err
	}

	endpoints := make([]interface{}, 0, len(specs))
	tunnelResolvers := make([]interface{}, 0)
	outboundBlocks := make([]interface{}, 0, len(specs))
	profileTags := make([]interface{}, 0, len(specs))
	hasOutbound := false
	for _, p := range specs {
		profileTags = append(profileTags, p.Tag)
		if p.Kind == kindOutbound {
			healOutboundConfig(p.Config)
			if autoMode {
				applyAutoDialBudget(p.Config)
			}
			outboundBlocks = append(outboundBlocks, p.Config)
			hasOutbound = true
		} else {
			ep, resolver := singBoxEndpoint(p.Config)
			endpoints = append(endpoints, ep)
			if resolver != nil {
				tunnelResolvers = append(tunnelResolvers, resolver)
			}
		}
	}

	cfg["endpoints"] = endpoints

	// Preserve system outbounds (direct/block/...) from the existing slice;
	// drop everything else — old profile outbounds and our own managed
	// groups (selector + urltest) get rebuilt from scratch below.
	preserved := make([]interface{}, 0)
	if existing, ok := cfg["outbounds"].([]interface{}); ok {
		for _, ob := range existing {
			m, _ := ob.(map[string]interface{})
			if m == nil {
				continue
			}
			tag, _ := m["tag"].(string)
			if systemOutboundTags[tag] && !managedGroupTags[tag] {
				preserved = append(preserved, m)
			}
		}
	}
	// Make sure a "direct" outbound always exists — required for ICMP
	// fall-through and the user whitelist.
	if !containsTag(preserved, "direct") {
		preserved = append(preserved, map[string]interface{}{"tag": "direct", "type": "direct"})
	}

	// Append every outbound-kind profile block, then the selector wrapping
	// ALL profile tags (endpoints + outbounds) with `default` = active.
	preserved = append(preserved, outboundBlocks...)
	if len(profileTags) > 0 {
		selector := map[string]interface{}{
			"tag":       SelectorTag,
			"type":      "selector",
			"outbounds": profileTags,
		}
		if activeTag != "" {
			selector["default"] = activeTag
		}
		preserved = append(preserved, selector)
		if autoMode {
			preserved = append(preserved, autoOutbound(profileTags))
		}
	}
	cfg["outbounds"] = preserved

	// Re-point route.final / dns.detour at whichever group is the head —
	// urltest when auto mode is on, the manual selector otherwise.
	head := SelectorTag
	if len(profileTags) == 0 {
		head = "direct"
	}
	if autoMode && len(profileTags) > 0 {
		head = AutoTag
	}
	rewriteRouteRefs(cfg, head, legacyTags)
	addTunnelResolvers(cfg, tunnelResolvers)
	applyHostNetwork(cfg, host)

	// Keep ICMP routed direct whenever ANY profile is outbound-kind —
	// the active selection might flip to one via Clash API at any time.
	ensureICMPDirectRule(cfg, hasOutbound)

	// Re-apply routing.json on top — owns route.rules (besides ICMP/prelude)
	// and route.final whenever proxy-mode is on. Missing routing.json yields
	// DefaultRoutingState which is effectively a no-op.
	if err := s.attachAutoTunnel(cfg); err != nil {
		return err
	}
	ApplyRouting(cfg, routing, head)

	return s.Write(cfg)
}

// HostNetwork is engine.HostNetwork for this package (it does not import
// the engine contract).
type HostNetwork struct {
	DirectInterface string
	DirectDNS       string
}

// applyHostNetwork points the direct path at what core/netdoctor chose.
// Applied on every rebuild, so a selection that went back to defaults is
// undone too. Runs after rewriteRouteRefs, which rewrites the dns block.
func applyHostNetwork(cfg map[string]interface{}, h HostNetwork) {
	if route, ok := cfg["route"].(map[string]interface{}); ok {
		if h.DirectInterface != "" {
			// sing-box ignores default_interface while auto-detect is on.
			route["auto_detect_interface"] = false
			route["default_interface"] = h.DirectInterface
		} else {
			route["auto_detect_interface"] = true
			delete(route, "default_interface")
		}
	}
	if h.DirectDNS == "" {
		return
	}
	dns, _ := cfg["dns"].(map[string]interface{})
	servers, _ := dns["servers"].([]interface{})
	for i, sv := range servers {
		if m, ok := sv.(map[string]interface{}); ok && m["tag"] == "dns-direct" {
			// A new-style server without detour dials like an empty
			// direct outbound — outside the tunnel, on the interface above.
			servers[i] = map[string]interface{}{
				"tag":    "dns-direct",
				"type":   "udp",
				"server": h.DirectDNS,
			}
		}
	}
}

func containsTag(arr []interface{}, tag string) bool {
	for _, it := range arr {
		m, _ := it.(map[string]interface{})
		if m == nil {
			continue
		}
		if t, _ := m["tag"].(string); t == tag {
			return true
		}
	}
	return false
}

// realIPv6SniffTimeout is how long a flow to a real IPv6 address may take to show
// its name. Only a flow that never sends a ClientHello waits it out, and with no
// name it could not have worked anyway.
const realIPv6SniffTimeout = "5s"

// realIPv6Rules handle a browser that connects to real IPv6 addresses: with
// Secure DNS it resolves past our fake IPs, and our TUN's IPv6 route makes it
// prefer them. The machine or the exit often has no IPv6. Once the TUN has
// accepted the TCP that failure reaches the browser as a closed connection,
// ERR_CONNECTION_CLOSED, and it never tries the site's IPv4.
//
// Russian ranges are refused first. These go direct, where the name no longer
// helps, and a name could take them into the tunnel, where our exits refuse them.
// This rule runs before any sniff, while the handshake is still ahead, so the
// browser sees a failed connect and moves on to the site's IPv4.
//
// Every other global address goes by the name it sniffed, so the exit picks the
// address. Global unicast only: LAN and fake addresses keep their routes. This
// sniff waits far longer than the core's default 300 ms: a ClientHello in two
// slow parts would otherwise run out here, the general sniff below would read the
// name without replacing the address, and the flow would reach the exit as IPv6.
func realIPv6Rules() []interface{} {
	ports := func() []interface{} { return []interface{}{80, 443, 8080, 8443} }
	var russian []string
	for _, cidr := range ruzone.CIDRs() {
		if strings.Contains(cidr, ":") {
			russian = append(russian, cidr)
		}
	}
	return []interface{}{
		map[string]interface{}{
			"action":  "reject",
			"ip_cidr": toAnySlice(russian),
			"network": "tcp",
			"port":    ports(),
		},
		map[string]interface{}{
			"action":               "sniff",
			"ip_cidr":              []interface{}{"2000::/3"},
			"override_destination": true,
			"port":                 ports(),
			"timeout":              realIPv6SniffTimeout,
		},
	}
}

// isRussianIPv6Reject recognises the first of realIPv6Rules. The other reject
// that names addresses, Secure DNS over QUIC, is UDP.
func isRussianIPv6Reject(m map[string]interface{}) bool {
	action, _ := m["action"].(string)
	network, _ := m["network"].(string)
	_, scoped := m["ip_cidr"]
	return action == "reject" && network == "tcp" && scoped
}

// healRealIPv6Rules puts the current rules ahead of the general sniff, in a
// config written before they existed or with an older list of ranges.
func healRealIPv6Rules(rules []interface{}) []interface{} {
	kept := make([]interface{}, 0, len(rules)+2)
	first := -1
	for _, r := range rules {
		if m, ok := r.(map[string]interface{}); ok {
			if isRussianIPv6Reject(m) {
				continue
			}
			if action, _ := m["action"].(string); action == "sniff" {
				if _, scoped := m["ip_cidr"]; scoped {
					continue
				}
				if first < 0 {
					first = len(kept)
				}
			}
		}
		kept = append(kept, r)
	}
	if first < 0 {
		return kept
	}
	out := make([]interface{}, 0, len(kept)+2)
	out = append(out, kept[:first]...)
	out = append(out, realIPv6Rules()...)
	return append(out, kept[first:]...)
}

// rewriteRouteRefs forces route.final / dns.detour onto `head` (selector
// or urltest tag). Rewrites anything currently pointed at one of our
// managed groups OR a legacy tag, leaving unrelated values alone.
func rewriteRouteRefs(cfg map[string]interface{}, head string, legacyTags map[string]bool) {
	shouldRewrite := func(v string) bool {
		return managedGroupTags[v] || legacyTags[v]
	}
	if route, ok := cfg["route"].(map[string]interface{}); ok {
		if v, _ := route["final"].(string); shouldRewrite(v) || v == "" {
			route["final"] = head
		}
		route["find_process"] = true
		if _, has := route["default_domain_resolver"]; !has {
			route["default_domain_resolver"] = map[string]interface{}{
				"server": "dns-direct",
			}
		}
		// Ensure sniff does not stall UDP VoIP/game ports (Discord, WebRTC).
		if rules, ok := route["rules"].([]interface{}); ok {
			for i, r := range rules {
				if m, ok := r.(map[string]interface{}); ok {
					if action, _ := m["action"].(string); action == "sniff" {
						m["port"] = []interface{}{80, 443, 8080, 8443}
						rules[i] = m
					}
				}
			}
			route["rules"] = healRealIPv6Rules(rules)
		}
	}
	// Always enforce canonical FakeIP DNS (IPv4 fake addresses, IPv6 only for
	// names without an A record, local direct resolver). Eliminates UDP 53
	// foreign drops by TSPU and dual-stack ICE timeouts in Discord.
	cfg["dns"] = canonicalDNS()
	exp, ok := cfg["experimental"].(map[string]interface{})
	if !ok {
		exp = map[string]interface{}{}
		cfg["experimental"] = exp
	}
	exp["clash_api"] = map[string]interface{}{
		"external_controller": "127.0.0.1:9090",
	}
	exp["cache_file"] = map[string]interface{}{
		"enabled":      true,
		"path":         "cache.db",
		"store_fakeip": true,
	}
	cfg["log"] = singboxLogBlock()
	// Always heal tun-in inbound for optimal MTU (1360), mixed stack, and FullCone NAT.
	cfg["inbounds"] = []interface{}{
		tunInbound(),
	}
}

// healOutboundConfig ensures VLESS profiles have proper packet_encoding.
func healOutboundConfig(cfg map[string]interface{}) {
	if cfg == nil {
		return
	}
	t, _ := cfg["type"].(string)
	if t != "vless" {
		return
	}
	if pe, _ := cfg["packet_encoding"].(string); pe == "" {
		cfg["packet_encoding"] = "xudp"
	}
}

// autoDialBudget caps a dial when there is another server to go to. A path that
// went silent gives no error to react to, so this is the time the first request
// waits before the group moves on. The core's default for a TLS handshake is 15s,
// fair with one server and far too long with a fallback ready.
const autoDialBudget = "5s"

func applyAutoDialBudget(cfg map[string]interface{}) {
	cfg["connect_timeout"] = autoDialBudget
	if tls, ok := cfg["tls"].(map[string]interface{}); ok {
		tls["handshake_timeout"] = autoDialBudget
	}
}

// autoProbeInterval is how often the group probes the node it holds. In failover
// mode that is one request per tick, whatever the size of the subscription. It
// only has to catch a node dying while nothing is using it: under traffic a
// connection left without an answer triggers a probe at once (patch 0002).
const autoProbeInterval = "120s"

// autoOutbound builds the `urltest` block behind auto mode. mode=failover holds
// a working node instead of chasing the fastest one, so ordinary jitter never
// changes the exit IP: the group leaves only when the node stops answering, when
// a live dial through it times out, or when it turns at least 2x slower than a
// neighbour and a fresh run confirms one 1.5x faster. Established connections stay on
// their node across a move (interrupt_exist_connections stays off).
func autoOutbound(profileTags []interface{}) map[string]interface{} {
	return map[string]interface{}{
		"tag":          AutoTag,
		"type":         "urltest",
		"mode":         "failover",
		"outbounds":    profileTags,
		"url":          "http://cp.cloudflare.com/generate_204",
		"interval":     autoProbeInterval,
		"idle_timeout": "30m",
	}
}

// NeedsSelectorMigration reports whether config.json is in the pre-selector
// layout — i.e. has no outbound tagged SelectorTag. Called once at engine
// init to trigger a RebuildActive even if profiles.json wasn't touched.
func (s *Store) NeedsSelectorMigration() bool {
	cfg, err := s.Read()
	if err != nil {
		return false
	}
	obs, _ := cfg["outbounds"].([]interface{})
	for _, ob := range obs {
		m, _ := ob.(map[string]interface{})
		if m == nil {
			continue
		}
		if t, _ := m["tag"].(string); t == SelectorTag {
			return false
		}
	}
	return true
}

// ApplyActive — DEPRECATED, kept until callers migrate. See RebuildActive.
//
//   - kind == "endpoint": writes block into cfg["endpoints"][0] and removes any
//     previously-active outbound (the one tagged outboundTag).
//   - kind == "outbound": appends/replaces the block in cfg["outbounds"]
//     (preserving system outbounds like "direct") and clears cfg["endpoints"].
//
// activeTags is the set of values currently sitting in route.final / detour
// that should be rewritten to newTag. Pass profile.ActiveTags.
func (s *Store) ApplyActive(kind string, block map[string]interface{}, newTag string, activeTags map[string]bool, outboundTag string) error {
	cfg, err := s.Read()
	if err != nil {
		return err
	}
	switch kind {
	case "outbound":
		cfg["endpoints"] = []interface{}{}
		cfg["outbounds"] = upsertTaggedOutbound(cfg["outbounds"], outboundTag, block)
		addTunnelResolvers(cfg, nil) // their detours were the endpoints
		// Hysteria2/VLESS/Trojan don't carry ICMP — without an explicit
		// route rule sing-box logs a WARN for every ping and the packets
		// die. Send ICMP to the system "direct" outbound so ping/tracert
		// behave like the user expects. Endpoint-shaped profiles (WG/AWG)
		// don't need this — they handle ICMP natively.
		ensureICMPDirectRule(cfg, true)
	case "endpoint":
		ep, resolver := singBoxEndpoint(block)
		cfg["endpoints"] = []interface{}{ep}
		if resolver != nil {
			addTunnelResolvers(cfg, []interface{}{resolver})
		}
		cfg["outbounds"] = removeTaggedOutbound(cfg["outbounds"], outboundTag)
		ensureICMPDirectRule(cfg, false)
	default:
		return fmt.Errorf("unknown profile kind: %s", kind)
	}
	rewriteActiveTagRefs(cfg, activeTags, newTag)
	return s.Write(cfg)
}

// ensureICMPDirectRule keeps a single `{network:"icmp", outbound:"direct"}`
// rule in route.rules in sync with whether we want it. With enable=true the
// rule is inserted right after the sniff/hijack-dns prelude so it matches
// before any domain/process rules; with enable=false any existing copy is
// removed.
func ensureICMPDirectRule(cfg map[string]interface{}, enable bool) {
	route, _ := cfg["route"].(map[string]interface{})
	if route == nil {
		if !enable {
			return
		}
		route = map[string]interface{}{}
		cfg["route"] = route
	}
	rules, _ := route["rules"].([]interface{})

	// Drop any existing copies first — keeps the function idempotent and
	// also handles enable=false.
	cleaned := make([]interface{}, 0, len(rules))
	for _, r := range rules {
		m, ok := r.(map[string]interface{})
		if !ok {
			cleaned = append(cleaned, r)
			continue
		}
		if isSystemDirectRule(m) {
			continue
		}
		cleaned = append(cleaned, m)
	}
	if !enable {
		route["rules"] = cleaned
		return
	}
	icmpRule := map[string]interface{}{
		"network":  "icmp",
		"outbound": "direct",
	}
	// Insert after the prelude (sniff / hijack-dns), if present.
	insertAt := 0
	for i, r := range cleaned {
		m, ok := r.(map[string]interface{})
		if !ok {
			break
		}
		action, _ := m["action"].(string)
		if action == "sniff" || action == "hijack-dns" || isRussianIPv6Reject(m) {
			insertAt = i + 1
			continue
		}
		break
	}
	// Private/LAN destinations never go through the tunnel: the exit server
	// cannot reach the user's home network, and with strict_route on Windows
	// every printer, NAS, LAN game or Delivery Optimization peer otherwise
	// ends up dialed from the VPN server.
	lanRule := map[string]interface{}{
		"ip_is_private": true,
		"outbound":      "direct",
	}
	// QUIC (UDP 443) is rejected so browsers fall back to TCP within a
	// second. Through the xhttp transport UDP rides inside TCP: QUIC's own
	// retransmits and congestion control stack on top of TCP's, and every
	// QUIC flow is one more xhttp session sharing the connections voice uses.
	quicRule := map[string]interface{}{
		"network": "udp",
		"port":    443,
		"action":  "reject",
	}
	out := make([]interface{}, 0, len(cleaned)+3)
	out = append(out, cleaned[:insertAt]...)
	out = append(out, icmpRule, lanRule, quicRule)
	out = append(out, cleaned[insertAt:]...)
	route["rules"] = out
}

// isSystemDirectRule reports whether m is one of the rules
// ensureICMPDirectRule owns (ICMP/LAN direct, QUIC reject). ApplyRouting
// preserves them.
func isSystemDirectRule(m map[string]interface{}) bool {
	if action, _ := m["action"].(string); action == "reject" {
		return isQUICReject(m)
	}
	if ob, _ := m["outbound"].(string); ob != "direct" {
		return false
	}
	if net, _ := m["network"].(string); net == "icmp" {
		return true
	}
	if lan, _ := m["ip_is_private"].(bool); lan {
		return true
	}
	return false
}

// isQUICReject reports whether m is the UDP 443 reject ensureICMPDirectRule
// adds. Port arrives as int when built here and float64 after a JSON round
// trip.
func isQUICReject(m map[string]interface{}) bool {
	if action, _ := m["action"].(string); action != "reject" {
		return false
	}
	net, _ := m["network"].(string)
	port, _ := m["port"].(int)
	if pf, ok := m["port"].(float64); ok {
		port = int(pf)
	}
	return net == "udp" && port == 443
}

// upsertTaggedOutbound returns the outbounds list with the entry tagged
// `tag` replaced by `block` (or appended if absent). Non-map entries pass
// through untouched.
func upsertTaggedOutbound(v interface{}, tag string, block map[string]interface{}) []interface{} {
	arr, _ := v.([]interface{})
	out := make([]interface{}, 0, len(arr)+1)
	replaced := false
	for _, it := range arr {
		ob, ok := it.(map[string]interface{})
		if !ok {
			out = append(out, it)
			continue
		}
		if t, _ := ob["tag"].(string); t == tag {
			out = append(out, block)
			replaced = true
			continue
		}
		out = append(out, ob)
	}
	if !replaced {
		out = append(out, block)
	}
	return out
}

// removeTaggedOutbound drops the outbound with the given tag if present.
func removeTaggedOutbound(v interface{}, tag string) []interface{} {
	arr, _ := v.([]interface{})
	out := make([]interface{}, 0, len(arr))
	for _, it := range arr {
		ob, ok := it.(map[string]interface{})
		if !ok {
			out = append(out, it)
			continue
		}
		if t, _ := ob["tag"].(string); t == tag {
			continue
		}
		out = append(out, ob)
	}
	return out
}

// rewriteActiveTagRefs walks the three known places where the active profile's
// tag is referenced (route.final, dns.final, dns.servers[].detour) and rewrites
// any value contained in `activeTags` to `newTag`. Leaves unrelated tags
// (e.g. system outbound "direct") alone.
func rewriteActiveTagRefs(cfg map[string]interface{}, activeTags map[string]bool, newTag string) {
	if route, ok := cfg["route"].(map[string]interface{}); ok {
		if v, _ := route["final"].(string); activeTags[v] {
			route["final"] = newTag
		}
	}
	dns, ok := cfg["dns"].(map[string]interface{})
	if !ok {
		return
	}
	if v, _ := dns["final"].(string); activeTags[v] {
		dns["final"] = newTag
	}
	servers, _ := dns["servers"].([]interface{})
	for _, srv := range servers {
		m, ok := srv.(map[string]interface{})
		if !ok {
			continue
		}
		if v, _ := m["detour"].(string); activeTags[v] {
			m["detour"] = newTag
		}
	}
}

// stripLineComments removes whole-line // comments so encoding/json can parse
// our JSONC config. Values never contain "//".
func stripLineComments(raw []byte) []byte {
	lines := strings.Split(string(raw), "\n")
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), "//") {
			continue
		}
		out = append(out, ln)
	}
	return []byte(strings.Join(out, "\n"))
}

// singBoxEndpoint is an endpoint as sing-box reads it, with the DNS server
// that resolves the names it dials, if it has one. A profile keeps "awg" as its
// type, which is what says AmneziaWG to the rest of the client, while the
// sing-box-lx core reads it as a "wireguard" endpoint with the obfuscation
// fields, and refuses the whole config over a type it does not know. The same
// refusal hits a peer's "preshared_key": 1.0.34 and earlier wrote the key under
// that name and the core knows only "pre_shared_key", and the profile's "dns",
// the [Interface] DNS line, which is the client's and not the core's. The
// stored profile is never changed: the endpoint is always a copy.
func singBoxEndpoint(block map[string]interface{}) (map[string]interface{}, map[string]interface{}) {
	typ := block["type"]
	if typ != "awg" && typ != "wireguard" {
		return block, nil
	}
	out := make(map[string]interface{}, len(block))
	for k, v := range block {
		out[k] = v
	}
	out["type"] = "wireguard"
	if peers, renamed := corePeers(block["peers"]); renamed {
		out["peers"] = peers
	}
	delete(out, "dns")
	delete(out, "destination_resolver")
	resolver := tunnelResolver(block)
	if resolver != nil {
		strategy := "ipv4_only"
		if tunnelCarries(block["address"], true) {
			strategy = "prefer_ipv4"
		}
		out["destination_resolver"] = map[string]interface{}{
			"server":   resolver["tag"],
			"strategy": strategy,
		}
	}
	return out, resolver
}

// tunnelResolverPrefix marks the DNS servers the engine adds per WireGuard
// endpoint; a rebuild drops every one of them before adding the current set.
const tunnelResolverPrefix = "dns-in-"

// defaultTunnelDNS is what a config with no DNS line resolves through: the
// Amnezia client's own default primary DNS (Cloudflare), reached through the
// tunnel like the configured one would be.
var defaultTunnelDNS = map[bool]string{false: "1.1.1.1", true: "2606:4700:4700::1111"}

// tunnelResolver is the DNS server for the names a WireGuard endpoint dials
// (patch 0006, destination_resolver): the config's DNS, or Cloudflare without
// one, asked through the endpoint itself. A name that reaches a WireGuard
// profile by name (a fake address the TUN handed out, the delay test, a port the
// sniffer skips) is resolved by the endpoint, and without its own server the
// DNS rules sent it to dns.final, the provider's resolver: the provider saw the
// name and could answer it with a stub, while the traffic itself went through
// the tunnel. The Amnezia client sets the config's DNS on the tunnel interface
// and blocks port 53 elsewhere for the same reason. A server the tunnel cannot
// carry (AllowedIPs leave it out, or the family has no tunnel address) is
// skipped for the next one; with none left, a split tunnel that carries no
// resolver at all, the endpoint keeps the DNS rules, as in the Amnezia client,
// where such a server is reached outside the tunnel.
func tunnelResolver(block map[string]interface{}) map[string]interface{} {
	tag, _ := block["tag"].(string)
	if tag == "" {
		return nil
	}
	candidates := make([]string, 0, 3)
	if list, ok := block["dns"].([]interface{}); ok {
		for _, item := range list {
			if s, ok := item.(string); ok {
				candidates = append(candidates, s)
			}
		}
	}
	// Cloudflare stands behind the config's servers too: one the tunnel
	// cannot carry (an IPv6 server on an IPv4-only tunnel, say) would otherwise
	// leave the names with the provider.
	candidates = append(candidates, defaultTunnelDNS[false], defaultTunnelDNS[true])
	allowed := peerAllowedIPs(block["peers"])
	for _, candidate := range candidates {
		addr, err := netip.ParseAddr(candidate)
		if err != nil {
			continue
		}
		addr = addr.Unmap()
		if !tunnelCarries(block["address"], addr.Is6()) || !prefixesContain(allowed, addr) {
			continue
		}
		return map[string]interface{}{
			"tag":    tunnelResolverPrefix + tag,
			"type":   "udp",
			"server": addr.String(),
			"detour": tag,
		}
	}
	return nil
}

// tunnelCarries says whether the endpoint has a tunnel address of the family.
func tunnelCarries(addresses interface{}, v6 bool) bool {
	for _, p := range parsePrefixes(addresses) {
		if p.Addr().Is6() == v6 {
			return true
		}
	}
	return false
}

func peerAllowedIPs(raw interface{}) []netip.Prefix {
	peers, _ := raw.([]interface{})
	var out []netip.Prefix
	for _, p := range peers {
		if peer, ok := p.(map[string]interface{}); ok {
			out = append(out, parsePrefixes(peer["allowed_ips"])...)
		}
	}
	return out
}

func parsePrefixes(raw interface{}) []netip.Prefix {
	var items []string
	switch v := raw.(type) {
	case string:
		items = []string{v}
	case []interface{}:
		for _, item := range v {
			if s, ok := item.(string); ok {
				items = append(items, s)
			}
		}
	case []string:
		items = v
	}
	out := make([]netip.Prefix, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if p, err := netip.ParsePrefix(item); err == nil {
			out = append(out, p.Masked())
		} else if a, err := netip.ParseAddr(item); err == nil {
			out = append(out, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
		}
	}
	return out
}

func prefixesContain(prefixes []netip.Prefix, addr netip.Addr) bool {
	for _, p := range prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// addTunnelResolvers replaces the per-endpoint DNS servers in dns.servers with
// the given set. They answer only the endpoints' destination_resolver: no DNS
// rule names them, so TUN queries keep fakeip and dns-direct.
func addTunnelResolvers(cfg map[string]interface{}, resolvers []interface{}) {
	dns, _ := cfg["dns"].(map[string]interface{})
	if dns == nil {
		if len(resolvers) == 0 {
			return
		}
		dns = map[string]interface{}{}
		cfg["dns"] = dns
	}
	servers, _ := dns["servers"].([]interface{})
	kept := make([]interface{}, 0, len(servers)+len(resolvers))
	for _, sv := range servers {
		if m, ok := sv.(map[string]interface{}); ok {
			if tag, _ := m["tag"].(string); strings.HasPrefix(tag, tunnelResolverPrefix) {
				continue
			}
		}
		kept = append(kept, sv)
	}
	dns["servers"] = append(kept, resolvers...)
}

// corePeers returns copies of the peers with "preshared_key" renamed, and
// whether any peer had it.
func corePeers(raw interface{}) ([]interface{}, bool) {
	peers, _ := raw.([]interface{})
	out := make([]interface{}, len(peers))
	renamed := false
	for i, p := range peers {
		out[i] = p
		peer, ok := p.(map[string]interface{})
		if !ok {
			continue
		}
		psk, ok := peer["preshared_key"]
		if !ok {
			continue
		}
		renamed = true
		copied := make(map[string]interface{}, len(peer))
		for k, v := range peer {
			if k != "preshared_key" {
				copied[k] = v
			}
		}
		if _, has := copied["pre_shared_key"]; !has {
			copied["pre_shared_key"] = psk
		}
		out[i] = copied
	}
	return out, renamed
}
