package configfile

import (
	"NekkoVPN/core/autotunnel"
	"NekkoVPN/core/routing"
	"NekkoVPN/core/ruzone"
	"errors"
	"fmt"
	"io/fs"
)

type RoutingMode = routing.RoutingMode
type RoutingState = routing.RoutingState
type RouteList = routing.RouteList

const (
	ModeBypass = routing.ModeBypass
	ModeProxy  = routing.ModeProxy
)

func DefaultRoutingState() RoutingState             { return routing.DefaultRoutingState() }
func (s *Store) ReadRouting() (RoutingState, error) { return routing.New(s.routingPath).ReadRouting() }
func (s *Store) WriteRouting(state RoutingState) error {
	return routing.New(s.routingPath).WriteRouting(state)
}
func (s *Store) mutateRouting(fn func(*routing.Store) error) error {
	r := routing.New(s.routingPath)
	if err := fn(r); err != nil {
		return err
	}
	state, err := r.ReadRouting()
	if err != nil {
		return err
	}
	return s.applyRoutingToConfig(state)
}
func (s *Store) SetRoutingEnabled(on bool) error {
	return s.mutateRouting(func(r *routing.Store) error { return r.SetRoutingEnabled(on) })
}
func (s *Store) SetRoutingMode(mode RoutingMode) error {
	return s.mutateRouting(func(r *routing.Store) error { return r.SetRoutingMode(mode) })
}
func (s *Store) AddRouteRule(mode RoutingMode, category, value string) error {
	return s.mutateRouting(func(r *routing.Store) error { return r.AddRouteRule(mode, category, value) })
}
func (s *Store) RemoveRouteRule(mode RoutingMode, category, value string) error {
	return s.mutateRouting(func(r *routing.Store) error { return r.RemoveRouteRule(mode, category, value) })
}

// ApplyRouting mutates cfg in-place to reflect state. head is the
// outbound tag that represents "via VPN" — typically AutoTag when
// auto-fastest is on, else SelectorTag. The function:
//
//   - preserves any sniff/hijack-dns prelude rules, the ICMP/LAN→direct rules and the QUIC reject
//   - drops every other rule (they're owned by us)
//   - inserts one rule per non-empty category (domain_suffix / ip_cidr /
//     process_name / process_path_regex) for the active mode's list, pointed
//     at the right outbound
//   - sets route.final to "direct" (proxy mode + enabled) or head (otherwise)
func ApplyRouting(cfg map[string]interface{}, state RoutingState, head string) {
	state = state.Sanitize()
	route, _ := cfg["route"].(map[string]interface{})
	if route == nil {
		route = map[string]interface{}{}
		cfg["route"] = route
	}

	rules, _ := route["rules"].([]interface{})
	preserved := make([]interface{}, 0, len(rules))
	for _, r := range rules {
		m, ok := r.(map[string]interface{})
		if !ok {
			preserved = append(preserved, r)
			continue
		}
		if action, _ := m["action"].(string); action == "sniff" || action == "hijack-dns" {
			preserved = append(preserved, m)
			continue
		}
		if isSystemDirectRule(m) {
			preserved = append(preserved, m)
			continue
		}
		// everything else is ours from a previous compile — drop
	}

	policy := state.Policy()
	finalTag := head
	if policy.DefaultTarget == routing.TargetDirect {
		finalTag = "direct"
	}
	ruleOutbound := head
	if policy.Target == routing.TargetDirect {
		ruleOutbound = "direct"
	}
	list := policy.Rules

	var userRules []interface{}
	if state.Enabled && ruleOutbound != "" {
		appendRule := func(key string, vals []string) {
			if len(vals) == 0 {
				return
			}
			userRules = append(userRules, map[string]interface{}{
				key:        toAnySlice(vals),
				"outbound": ruleOutbound,
			})
		}
		folders := routing.FolderRegexes(list.Folders)
		appendRule("domain_suffix", list.Domains)
		appendRule("ip_cidr", list.IPs)
		// Between the two: a domain or address the user sent direct stays
		// direct, an app sent direct does not take its Secure DNS with it.
		if ruleOutbound == "direct" && len(list.Processes)+len(folders) > 0 {
			userRules = append(userRules, escapeDirectRules(route, head)...)
		}
		appendRule("process_name", list.Processes)
		appendRule("process_path_regex", folders)
	}
	// After the user's rules, so a Russian site the user sent through the VPN
	// on purpose stays there; ahead of the default and the escapes, so it never
	// falls into the tunnel by accident, where our exits refuse it.
	userRules = append(userRules, ruZoneRules()...)
	if state.Enabled && ruleOutbound != "" && finalTag == "direct" {
		userRules = append(userRules, escapeDirectRules(route, head)...)
	}

	// Direct rules go ahead of the QUIC reject: an app the user sent direct
	// must see the network exactly as without the VPN. Behind the reject a
	// bypassed game still lost all UDP 443 — EOS / PlayFab traffic among it.
	// With the tunnel as the default every compiled rule is a direct one.
	insertAt := len(preserved)
	if finalTag != "direct" {
		for i, r := range preserved {
			if m, ok := r.(map[string]interface{}); ok && isQUICReject(m) {
				insertAt = i
				break
			}
		}
	}
	out := make([]interface{}, 0, len(preserved)+len(userRules))
	out = append(out, preserved[:insertAt]...)
	out = append(out, userRules...)
	out = append(out, preserved[insertAt:]...)

	route["rules"] = out
	route["final"] = finalTag

	var bypass RouteList
	if ruleOutbound == "direct" {
		bypass = list
	}
	applyBypassDNS(cfg, bypass)
}

// ruZoneRules send the Russian internet direct (core/ruzone): our exits refuse
// it, so through the tunnel a Russian site does not open at all. The names
// match whatever resolves them; the addresses catch an app that connects by a
// real address it resolved itself, such as a browser with Secure DNS.
func ruZoneRules() []interface{} {
	return []interface{}{map[string]interface{}{
		"domain_suffix": toAnySlice(ruzone.Domains()),
		"ip_cidr":       toAnySlice(ruzone.CIDRs()),
		"outbound":      "direct",
	}}
}

// Public Secure DNS resolvers, as browsers reach them: by name, or by address
// when the browser has it built in.
var (
	secureDNSDomains = []string{
		"cloudflare-dns.com", "one.one.one.one", "dns.google", "quad9.net",
		"doh.opendns.com", "dns.nextdns.io", "dns.adguard-dns.com", "doh.cleanbrowsing.org",
	}
	secureDNSAddresses = []string{
		"1.1.1.1/32", "1.0.0.1/32", "8.8.8.8/32", "8.8.4.4/32", "9.9.9.9/32", "149.112.112.112/32",
	}
)

// secureDNSRules keep a browser's encrypted DNS in the tunnel when the browser
// itself goes direct. Censoring ISPs drop these resolvers, and a browser in
// Secure DNS mode has no other way to resolve a name: every site then fails
// with ERR_NAME_NOT_RESOLVED, though the sites themselves are reachable. The
// answers come back through the tunnel and the browser still connects to the
// sites directly.
//
// QUIC is refused, as for all tunnelled traffic, and the browser retries over
// TCP. Ports are limited to DoH and DoT: plain DNS is hijacked earlier, and
// the addresses serve nothing else that matters here.
func secureDNSRules(head string) []interface{} {
	match := func() map[string]interface{} {
		return map[string]interface{}{
			"domain_suffix": toAnySlice(secureDNSDomains),
			"ip_cidr":       toAnySlice(secureDNSAddresses),
			"port":          []interface{}{443, 853},
		}
	}
	reject := match()
	reject["network"] = "udp"
	reject["action"] = "reject"
	tunnel := match()
	tunnel["outbound"] = head
	return []interface{}{reject, tunnel}
}

// AutoTunnelTag is the rule set tag of the hosts that stay silent on the
// direct path (core/autotunnel). The constant lives there: svcproto.Sanitize
// checks the same value on the service side of the boundary.
const AutoTunnelTag = autotunnel.Tag

// attachAutoTunnel declares the rule set. The core refuses to start over a
// file that is missing, so it is created here, before any config can name it.
// The path is this store's own: what the service runs is a different copy,
// rewritten to a service-owned location by svcproto.Sanitize, with the hosts
// pushed over the control endpoint — see core/autotunnel.
func (s *Store) attachAutoTunnel(cfg map[string]interface{}) error {
	path := s.AutoTunnelPath()
	if err := autotunnel.EnsureRuleSet(path); err != nil {
		return fmt.Errorf("список автотуннеля: %w", err)
	}
	route, _ := cfg["route"].(map[string]interface{})
	if route == nil {
		route = map[string]interface{}{}
		cfg["route"] = route
	}
	route["rule_set"] = []interface{}{map[string]interface{}{
		"tag": AutoTunnelTag, "type": "local", "format": "source", "path": path,
	}}
	return nil
}

// escapeDirectRules are what leaves the direct path even though an app was sent
// there: Secure DNS, and the sites proven silent on it. The second only once the
// rule set is declared.
func escapeDirectRules(route map[string]interface{}, head string) []interface{} {
	rules := secureDNSRules(head)
	sets, _ := route["rule_set"].([]interface{})
	if len(sets) == 0 {
		return rules
	}
	// A port list on purpose: isQUICReject takes a single 443 for the QUIC
	// reject it preserves, and this rule is recompiled like the rest.
	return append(rules,
		map[string]interface{}{
			"rule_set": []interface{}{AutoTunnelTag}, "network": "udp", "port": []interface{}{443}, "action": "reject",
		},
		map[string]interface{}{"rule_set": []interface{}{AutoTunnelTag}, "outbound": head},
	)
}

// applyBypassDNS gives bypassed traffic real DNS answers instead of fake
// IPs — our equivalent of Clash's fake-ip-filter. A fake 198.18.x.x address
// is harmless for a browser but not for a game: matchmaking pings QoS
// beacons by resolved address and compares where replies come from, and
// that does not survive the core resolving the name a second time.
//
// Domains are what actually matters on Windows and macOS: apps there ask
// the system resolver (svchost / mDNSResponder), so the query reaching the
// core never carries the game's process name. The process_name rule only
// helps apps that resolve on their own.
//
// Our rules are the ones tagged bypassDNSMarker; they are dropped and
// re-added on every compile so the function stays idempotent.
func applyBypassDNS(cfg map[string]interface{}, bypass RouteList) {
	dns, _ := cfg["dns"].(map[string]interface{})
	if dns == nil {
		return
	}
	rules, _ := dns["rules"].([]interface{})
	kept := make([]interface{}, 0, len(rules)+2)
	if len(bypass.Domains) > 0 {
		kept = append(kept, map[string]interface{}{
			"domain_suffix": toAnySlice(bypass.Domains),
			"server":        "dns-direct",
		})
	}
	// The Russian internet goes direct whatever the mode (ruZoneRules), and
	// with real answers: Russian CDNs pick a node by the resolver's location.
	kept = append(kept, map[string]interface{}{
		"domain_suffix": toAnySlice(ruzone.Domains()),
		"server":        "dns-direct",
	})
	if len(bypass.Processes) > 0 {
		kept = append(kept, map[string]interface{}{
			"process_name": toAnySlice(bypass.Processes),
			"server":       "dns-direct",
		})
	}
	if len(bypass.Folders) > 0 {
		kept = append(kept, map[string]interface{}{
			"process_path_regex": toAnySlice(routing.FolderRegexes(bypass.Folders)),
			"server":             "dns-direct",
		})
	}
	for _, r := range rules {
		if m, ok := r.(map[string]interface{}); ok && (isBypassDNSRule(m) || isRUZoneDNSRule(m)) {
			continue
		}
		kept = append(kept, r)
	}
	dns["rules"] = kept
}

// isBypassDNSRule recognises the rules applyBypassDNS owns: any
// process_name or process_path_regex → dns-direct rule, and a domain_suffix → dns-direct rule
// other than the fixed ones: the VPN servers' own names and realAddressRule,
// which is the only rule carrying domain_regex.
func isBypassDNSRule(m map[string]interface{}) bool {
	if m["server"] != "dns-direct" {
		return false
	}
	if _, fixed := m["domain_regex"]; fixed {
		return false
	}
	if _, has := m["process_name"]; has {
		return true
	}
	if _, has := m["process_path_regex"]; has {
		return true
	}
	suf, has := m["domain_suffix"].([]interface{})
	if !has {
		return false
	}
	return !(len(suf) == 1 && suf[0] == "zovfy.ru") && !isRUZoneDNSRule(m)
}

// isRUZoneDNSRule recognises the Russian zone's dns-direct rule, which
// applyBypassDNS also owns but keeps whatever the routing state.
func isRUZoneDNSRule(m map[string]interface{}) bool {
	if m["server"] != "dns-direct" {
		return false
	}
	suf, _ := m["domain_suffix"].([]interface{})
	zone := ruzone.Domains()
	if len(suf) != len(zone) {
		return false
	}
	for i, d := range zone {
		if suf[i] != d {
			return false
		}
	}
	return true
}

func toAnySlice(vals []string) []interface{} {
	out := make([]interface{}, len(vals))
	for i, v := range vals {
		out[i] = v
	}
	return out
}

// applyRoutingToConfig reads config.json, runs ApplyRouting against it,
// writes it back. Missing config.json is tolerated — a future RebuildActive
// will pick up the routing state from routing.json on its own.
func (s *Store) applyRoutingToConfig(state RoutingState) error {
	cfg, err := s.Read()
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := s.attachAutoTunnel(cfg); err != nil {
		return err
	}
	ApplyRouting(cfg, state, currentHead(cfg))
	return s.Write(cfg)
}

// currentHead picks the "via VPN" outbound tag by inspecting the existing
// outbounds list: AutoTag if the urltest block is present, else SelectorTag.
// Used when applying routing standalone (outside RebuildActive, which knows
// the head directly).
func currentHead(cfg map[string]interface{}) string {
	obs, _ := cfg["outbounds"].([]interface{})
	for _, ob := range obs {
		m, _ := ob.(map[string]interface{})
		if m == nil {
			continue
		}
		if t, _ := m["tag"].(string); t == AutoTag {
			return AutoTag
		}
	}
	return SelectorTag
}
