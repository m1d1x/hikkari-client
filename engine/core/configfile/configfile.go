// Package configfile reads/writes the sing-box config.json and the
// "whitelist" (direct-bypass) route rules the UI exposes.
//
// The on-disk format is JSONC — we strip whole-line // comments before
// json.Unmarshal so the user can keep notes inline.
package configfile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func (s *Store) Path() string        { return s.path }
func (s *Store) RoutingPath() string { return s.routingPath }

func (s *Store) Read() (map[string]interface{}, error) {
	raw, err := os.ReadFile(s.path)
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
	return os.WriteFile(s.path, out, 0644)
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
	cfg, err := s.Read()
	if err != nil {
		return err
	}

	endpoints := make([]interface{}, 0, len(specs))
	outboundBlocks := make([]interface{}, 0, len(specs))
	profileTags := make([]interface{}, 0, len(specs))
	hasOutbound := false
	for _, p := range specs {
		profileTags = append(profileTags, p.Tag)
		if p.Kind == kindOutbound {
			outboundBlocks = append(outboundBlocks, p.Config)
			hasOutbound = true
		} else {
			endpoints = append(endpoints, p.Config)
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
	if autoMode && len(profileTags) > 0 {
		head = AutoTag
	}
	rewriteRouteRefs(cfg, head, legacyTags)

	// Keep ICMP routed direct whenever ANY profile is outbound-kind —
	// the active selection might flip to one via Clash API at any time.
	ensureICMPDirectRule(cfg, hasOutbound)

	// Re-apply routing.json on top — owns route.rules (besides ICMP/prelude)
	// and route.final whenever proxy-mode is on. Missing routing.json yields
	// DefaultRoutingState which is effectively a no-op.
	routing, err := s.ReadRouting()
	if err != nil {
		return err
	}
	ApplyRouting(cfg, routing, head)

	return s.Write(cfg)
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
	}
	dns, ok := cfg["dns"].(map[string]interface{})
	if !ok {
		return
	}
	if v, _ := dns["final"].(string); shouldRewrite(v) {
		dns["final"] = head
	}
	servers, _ := dns["servers"].([]interface{})
	for _, srv := range servers {
		m, ok := srv.(map[string]interface{})
		if !ok {
			continue
		}
		if v, _ := m["detour"].(string); shouldRewrite(v) {
			m["detour"] = head
		}
	}
}

// autoOutbound builds the `urltest` block that wraps every profile and
// continuously picks the fastest live one. Settings mirror what Karing /
// Stash ship by default; tolerance=50 means "don't flap unless the new
// candidate is at least 50ms faster than the current pick".
func autoOutbound(profileTags []interface{}) map[string]interface{} {
	return map[string]interface{}{
		"tag":       AutoTag,
		"type":      "urltest",
		"outbounds": profileTags,
		"url":       "http://cp.cloudflare.com/generate_204",
		"interval":  "30s",
		"tolerance": 50,
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
		// Hysteria2/VLESS/Trojan don't carry ICMP — without an explicit
		// route rule sing-box logs a WARN for every ping and the packets
		// die. Send ICMP to the system "direct" outbound so ping/tracert
		// behave like the user expects. Endpoint-shaped profiles (WG/AWG)
		// don't need this — they handle ICMP natively.
		ensureICMPDirectRule(cfg, true)
	case "endpoint":
		cfg["endpoints"] = []interface{}{block}
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
		net, _ := m["network"].(string)
		ob, _ := m["outbound"].(string)
		if net == "icmp" && ob == "direct" {
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
		if action == "sniff" || action == "hijack-dns" {
			insertAt = i + 1
			continue
		}
		break
	}
	out := make([]interface{}, 0, len(cleaned)+1)
	out = append(out, cleaned[:insertAt]...)
	out = append(out, icmpRule)
	out = append(out, cleaned[insertAt:]...)
	route["rules"] = out
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

