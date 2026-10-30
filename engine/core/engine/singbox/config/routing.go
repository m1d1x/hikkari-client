package configfile

import (
	"NekkoVPN/core/routing"
	"errors"
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
//     process_name) for the active mode's list, pointed at the right outbound
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
		appendRule("domain_suffix", list.Domains)
		appendRule("ip_cidr", list.IPs)
		appendRule("process_name", list.Processes)
	}

	// Bypass rules go ahead of the QUIC reject: an app the user sent direct
	// must see the network exactly as without the VPN. Behind the reject a
	// bypassed game still lost all UDP 443 — EOS / PlayFab traffic among it.
	insertAt := len(preserved)
	if ruleOutbound == "direct" {
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
	if len(bypass.Processes) > 0 {
		kept = append(kept, map[string]interface{}{
			"process_name": toAnySlice(bypass.Processes),
			"server":       "dns-direct",
		})
	}
	for _, r := range rules {
		if m, ok := r.(map[string]interface{}); ok && isBypassDNSRule(m) {
			continue
		}
		kept = append(kept, r)
	}
	dns["rules"] = kept
}

// isBypassDNSRule recognises the rules applyBypassDNS owns: any
// process_name → dns-direct rule, and a domain_suffix → dns-direct rule
// other than the fixed one that keeps the VPN servers' own names real.
func isBypassDNSRule(m map[string]interface{}) bool {
	if m["server"] != "dns-direct" {
		return false
	}
	if _, has := m["process_name"]; has {
		return true
	}
	suf, has := m["domain_suffix"].([]interface{})
	if !has {
		return false
	}
	return !(len(suf) == 1 && suf[0] == "zovfy.ru")
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
