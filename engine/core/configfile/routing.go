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
	if l.Domains == nil {
		l.Domains = []string{}
	}
	if l.IPs == nil {
		l.IPs = []string{}
	}
	if l.Processes == nil {
		l.Processes = []string{}
	}
	return l
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
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("пустое значение")
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
//   - preserves any sniff/hijack-dns prelude rules and the ICMP→direct rule
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
		if net, _ := m["network"].(string); net == "icmp" {
			if ob, _ := m["outbound"].(string); ob == "direct" {
				preserved = append(preserved, m)
				continue
			}
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

	if state.Enabled && ruleOutbound != "" {
		appendRule := func(key string, vals []string) {
			if len(vals) == 0 {
				return
			}
			anyVals := make([]interface{}, len(vals))
			for i, v := range vals {
				anyVals[i] = v
			}
			preserved = append(preserved, map[string]interface{}{
				key:        anyVals,
				"outbound": ruleOutbound,
			})
		}
		appendRule("domain_suffix", list.Domains)
		appendRule("ip_cidr", list.IPs)
		appendRule("process_name", list.Processes)
	}

	route["rules"] = preserved
	route["final"] = finalTag
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
