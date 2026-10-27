package configfile

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// RoutingMode is what the list in RoutingState means:
//
//   - ModeBypass — entries in the list go DIRECT, everything else through VPN.
//   - ModeProxy  — entries in the list go through VPN, everything else DIRECT.
type RoutingMode string

const (
	ModeBypass RoutingMode = "bypass"
	ModeProxy  RoutingMode = "proxy"
)

// RouteList holds the three category lists the UI exposes per mode.
type RouteList struct {
	Domains   []string `json:"domains"`
	IPs       []string `json:"ips"`
	Processes []string `json:"processes"`
}

// RoutingState is the authoritative split-routing config. Persisted to
// routing.json next to config.json; the sing-box route.rules are computed
// from this every time we apply.
type RoutingState struct {
	Enabled bool        `json:"enabled"`
	Mode    RoutingMode `json:"mode"`
	Bypass  RouteList   `json:"bypass"`
	Proxy   RouteList   `json:"proxy"`
}

// DefaultRoutingState is what we materialise when routing.json doesn't
// exist yet. Enabled+empty bypass list = identical behaviour to "off"
// (everything via VPN), but lets the UI display a meaningful default mode.
func DefaultRoutingState() RoutingState {
	return RoutingState{
		Enabled: true,
		Mode:    ModeBypass,
		Bypass:  RouteList{Domains: []string{}, IPs: []string{}, Processes: []string{}},
		Proxy:   RouteList{Domains: []string{}, IPs: []string{}, Processes: []string{}},
	}
}

// sanitize normalises a freshly-decoded RoutingState: nil slices become
// empty, unknown modes fall back to bypass. Safe to call on Default too.
func (s RoutingState) sanitize() RoutingState {
	if s.Mode != ModeBypass && s.Mode != ModeProxy {
		s.Mode = ModeBypass
	}
	s.Bypass = sanitizeList(s.Bypass)
	s.Proxy = sanitizeList(s.Proxy)
	return s
}

func sanitizeList(l RouteList) RouteList {
	l.Domains = keepValid("domains", l.Domains)
	l.IPs = keepValid("ips", l.IPs)
	l.Processes = keepValid("processes", l.Processes)
	return l
}

// keepValid drops entries that no longer compile into a usable rule, and
// never returns nil. Dropping is deliberate: an unparseable ip_cidr makes
// sing-box exit FATAL on every start, so keeping such a value would leave the
// tunnel permanently dead with no way for the user to edit it out. Only
// routing.json files written before normalizeRouteValue existed can contain
// one — AddRouteRule rejects them at the source now.
func keepValid(category string, vals []string) []string {
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		if norm, err := normalizeRouteValue(category, v); err == nil {
			out = append(out, norm)
		}
	}
	return out
}

// ReadRouting loads routing.json. Missing file returns DefaultRoutingState
// and a nil error — fresh installs are not an error.
func (s *Store) ReadRouting() (RoutingState, error) {
	raw, err := os.ReadFile(s.routingPath)
	if errors.Is(err, fs.ErrNotExist) {
		return DefaultRoutingState(), nil
	}
	if err != nil {
		return RoutingState{}, err
	}
	var state RoutingState
	if err := json.Unmarshal(raw, &state); err != nil {
		return RoutingState{}, fmt.Errorf("routing.json parse: %w", err)
	}
	return state.sanitize(), nil
}

// WriteRouting persists routing.json. Caller usually follows with
// applyRoutingToConfig so the live sing-box config matches.
func (s *Store) WriteRouting(state RoutingState) error {
	out, err := json.MarshalIndent(state.sanitize(), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.routingPath, out, 0644)
}

// SetRoutingEnabled flips the master toggle and recompiles config.json.
func (s *Store) SetRoutingEnabled(on bool) error {
	state, err := s.ReadRouting()
	if err != nil {
		return err
	}
	state.Enabled = on
	if err := s.WriteRouting(state); err != nil {
		return err
	}
	return s.applyRoutingToConfig(state)
}

// SetRoutingMode switches between bypass and proxy semantics. Each mode
// keeps its own list, so the user's data isn't lost on toggle.
func (s *Store) SetRoutingMode(mode RoutingMode) error {
	if mode != ModeBypass && mode != ModeProxy {
		return fmt.Errorf("неизвестный режим: %s", mode)
	}
	state, err := s.ReadRouting()
	if err != nil {
		return err
	}
	state.Mode = mode
	if err := s.WriteRouting(state); err != nil {
		return err
	}
	return s.applyRoutingToConfig(state)
}

// AddRouteRule appends a value to the (mode, category) list. Dedup is
// case-insensitive to match the original whitelist behaviour.
func (s *Store) AddRouteRule(mode RoutingMode, category, value string) error {
	value, err := normalizeRouteValue(category, value)
	if err != nil {
		return err
	}
	state, err := s.ReadRouting()
	if err != nil {
		return err
	}
	list, err := pickList(&state, mode)
	if err != nil {
		return err
	}
	if err := addToList(list, category, value); err != nil {
		return err
	}
	if err := s.WriteRouting(state); err != nil {
		return err
	}
	return s.applyRoutingToConfig(state)
}

// RemoveRouteRule removes a value from the (mode, category) list.
// Removing something that isn't there is a no-op, not an error.
func (s *Store) RemoveRouteRule(mode RoutingMode, category, value string) error {
	// Normalise when possible so "1.2.3.4" deletes the stored "1.2.3.4/32";
	// an unnormalisable value is still passed through, since a legacy entry
	// has to stay deletable by its literal text.
	if norm, err := normalizeRouteValue(category, value); err == nil {
		value = norm
	}
	state, err := s.ReadRouting()
	if err != nil {
		return err
	}
	list, err := pickList(&state, mode)
	if err != nil {
		return err
	}
	if err := removeFromList(list, category, value); err != nil {
		return err
	}
	if err := s.WriteRouting(state); err != nil {
		return err
	}
	return s.applyRoutingToConfig(state)
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
	state = state.sanitize()
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

	finalTag := head
	ruleOutbound := ""
	var list RouteList
	if state.Enabled {
		switch state.Mode {
		case ModeBypass:
			list = state.Bypass
			ruleOutbound = "direct"
		case ModeProxy:
			list = state.Proxy
			ruleOutbound = head
			finalTag = "direct"
		}
	}

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

	var directProcs []string
	if ruleOutbound == "direct" {
		directProcs = list.Processes
	}
	applyBypassDNS(cfg, directProcs)
}

// applyBypassDNS gives bypassed processes real DNS answers instead of fake
// IPs. A fake 198.18.x.x address is harmless for a browser but not for a
// game: matchmaking hands server names around, pings QoS beacons by
// resolved address and compares where replies come from, and none of that
// survives the core resolving the name a second time on its own. Clash in
// system-proxy mode — the setup that worked — never touches game DNS at all.
//
// The rule is ours whenever it is a process_name → dns-direct rule; it is
// dropped and re-added on every compile so the function stays idempotent.
func applyBypassDNS(cfg map[string]interface{}, procs []string) {
	dns, _ := cfg["dns"].(map[string]interface{})
	if dns == nil {
		return
	}
	rules, _ := dns["rules"].([]interface{})
	kept := make([]interface{}, 0, len(rules)+1)
	if len(procs) > 0 {
		kept = append(kept, map[string]interface{}{
			"process_name": toAnySlice(procs),
			"server":       "dns-direct",
		})
	}
	for _, r := range rules {
		if m, ok := r.(map[string]interface{}); ok {
			if _, has := m["process_name"]; has && m["server"] == "dns-direct" {
				continue
			}
		}
		kept = append(kept, r)
	}
	dns["rules"] = kept
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

func pickList(state *RoutingState, mode RoutingMode) (*RouteList, error) {
	switch mode {
	case ModeBypass:
		return &state.Bypass, nil
	case ModeProxy:
		return &state.Proxy, nil
	default:
		return nil, fmt.Errorf("неизвестный режим: %s", mode)
	}
}

func addToList(list *RouteList, category, value string) error {
	switch category {
	case "domains":
		if !containsCI(list.Domains, value) {
			list.Domains = append(list.Domains, value)
		}
	case "ips":
		if !containsCI(list.IPs, value) {
			list.IPs = append(list.IPs, value)
		}
	case "processes":
		if !containsCI(list.Processes, value) {
			list.Processes = append(list.Processes, value)
		}
	default:
		return fmt.Errorf("неизвестная категория: %s", category)
	}
	return nil
}

func removeFromList(list *RouteList, category, value string) error {
	drop := func(in []string) []string {
		out := in[:0]
		for _, v := range in {
			if !strings.EqualFold(v, value) {
				out = append(out, v)
			}
		}
		return out
	}
	switch category {
	case "domains":
		list.Domains = drop(list.Domains)
	case "ips":
		list.IPs = drop(list.IPs)
	case "processes":
		list.Processes = drop(list.Processes)
	default:
		return fmt.Errorf("неизвестная категория: %s", category)
	}
	return nil
}

func containsCI(haystack []string, needle string) bool {
	for _, v := range haystack {
		if strings.EqualFold(v, needle) {
			return true
		}
	}
	return false
}
