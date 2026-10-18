// Package profile manages profiles.json — the list of VPN servers the user
// can switch between. Switching a profile swaps either cfg["endpoints"] (for
// WireGuard-shaped protocols) or one entry in cfg["outbounds"] (for
// outbound-shaped protocols like Hysteria2/VLESS); route/dns/whitelist rules
// stay shared across servers.
package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Kind tells SwitchProfile which sing-box section to write the active profile
// into. "endpoint" → cfg["endpoints"][0]; "outbound" → append/replace in
// cfg["outbounds"] keeping the system outbounds (direct/block) in place.
const (
	KindEndpoint = "endpoint"
	KindOutbound = "outbound"
)

// SelectorTag is the single sing-box outbound that route.final and
// dns.detour point at. It's a `selector` group that lists every profile;
// switching profiles is a Clash-API call against this tag, not a config
// rewrite — so it never changes once written.
const SelectorTag = "proxy"

// Historical pre-selector tag values. RebuildActive only references them
// when scrubbing legacy configs during migration.
const (
	SharedEndpointTag = "awg-ep"
	SharedOutboundTag = "proxy-out"
)

// LegacyActiveTags is the set of tag values that pre-selector config files
// may have in route.final / dns.detour. Migration rewrites any of these to
// SelectorTag.
var LegacyActiveTags = map[string]bool{
	SharedEndpointTag: true,
	SharedOutboundTag: true,
}

// ProfileTag returns the canonical sing-box tag for a profile with the given
// `name`. Uses a "p-" prefix so we never collide with system outbounds like
// "direct" or "block".
func ProfileTag(name string) string { return "p-" + name }

// Spec is the minimum info configfile.RebuildActive needs per profile.
type Spec struct {
	Tag    string                 // ProfileTag(name)
	Kind   string                 // KindEndpoint | KindOutbound
	Config map[string]interface{} // sing-box block (Spec.Tag is forced onto it)
	Name   string                 // raw profile name (for debugging / logs)
}

// Profile is one server entry stored in profiles.json. Config is the
// sing-box-shaped block (endpoint object for KindEndpoint, outbound object
// for KindOutbound).
type Profile struct {
	Name   string                 `json:"name"`
	Label  string                 `json:"label"`
	Kind   string                 `json:"kind"`
	Config map[string]interface{} `json:"config"`
}

// UnmarshalJSON tolerates the legacy on-disk shape that used "endpoint"
// instead of "config" and had no "kind" field at all. Anything legacy is
// treated as KindEndpoint.
func (p *Profile) UnmarshalJSON(data []byte) error {
	var r struct {
		Name     string                 `json:"name"`
		Label    string                 `json:"label"`
		Kind     string                 `json:"kind"`
		Config   map[string]interface{} `json:"config"`
		Endpoint map[string]interface{} `json:"endpoint"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	p.Name = r.Name
	p.Label = r.Label
	p.Kind = r.Kind
	p.Config = r.Config
	if p.Config == nil && r.Endpoint != nil {
		p.Config = r.Endpoint
	}
	if p.Kind == "" {
		p.Kind = KindEndpoint
	}
	return nil
}

// File is the on-disk shape of profiles.json.
type File struct {
	Active string `json:"active"`
	// Auto, when true, makes the config emit a `urltest` outbound that
	// wraps every profile and continuously picks the fastest live one.
	// route.final then targets the urltest instead of the manual selector.
	Auto     bool      `json:"auto,omitempty"`
	Profiles []Profile `json:"profiles"`
}

// Info is the lightweight projection the UI renders.
type Info struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Active   bool   `json:"active"`
	Endpoint string `json:"endpoint"`
	Protocol string `json:"protocol"`
}

// Store is a thin file-backed profile repository.
type Store struct {
	path string
}

func New(path string) *Store { return &Store{path: path} }

func (s *Store) Path() string { return s.path }

// Read loads profiles.json. A missing file is not an error: a fresh install
// has no profiles yet, and the very first Add (an import link, say) has to
// work without anything having seeded the file first — it materialises on
// the following Write.
func (s *Store) Read() (*File, error) {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return &File{Profiles: []Profile{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var pf File
	if err := json.Unmarshal(raw, &pf); err != nil {
		return nil, fmt.Errorf("profiles.json parse: %w", err)
	}
	return &pf, nil
}

func (s *Store) Write(pf *File) error {
	out, err := json.MarshalIndent(pf, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	return os.WriteFile(s.path, out, 0644)
}

// Exists reports whether profiles.json is already present on disk.
func (s *Store) Exists() bool {
	_, err := os.Stat(s.path)
	return err == nil
}

// SeedFromEndpoint creates profiles.json with a single "main" profile built
// from the supplied endpoint map. Used on first launch so the UI always has
// something to render.
func (s *Store) SeedFromEndpoint(ep map[string]interface{}) error {
	return s.Write(&File{
		Active: "main",
		Profiles: []Profile{{
			Name:   "main",
			Label:  "Основной",
			Kind:   KindEndpoint,
			Config: ep,
		}},
	})
}

// List returns all profiles projected to the UI shape.
func (s *Store) List() ([]Info, error) {
	pf, err := s.Read()
	if err != nil {
		return nil, err
	}
	out := make([]Info, 0, len(pf.Profiles))
	for _, p := range pf.Profiles {
		out = append(out, Info{
			Name:     p.Name,
			Label:    p.Label,
			Active:   p.Name == pf.Active,
			Endpoint: peerEndpointForProfile(p),
			Protocol: prettyProtocol(p),
		})
	}
	return out, nil
}

// Get returns the full stored profile by name. ok is false when no profile
// with that name exists (not an error — the caller decides how to report it).
func (s *Store) Get(name string) (Profile, bool, error) {
	pf, err := s.Read()
	if err != nil {
		return Profile{}, false, err
	}
	for _, p := range pf.Profiles {
		if p.Name == name {
			return p, true, nil
		}
	}
	return Profile{}, false, nil
}

func (s *Store) Active() (string, error) {
	pf, err := s.Read()
	if err != nil {
		return "", err
	}
	return pf.Active, nil
}

// AutoMode returns whether auto-fastest selection is enabled.
func (s *Store) AutoMode() (bool, error) {
	pf, err := s.Read()
	if err != nil {
		return false, err
	}
	return pf.Auto, nil
}

// SetAutoMode flips the auto-fastest flag on profiles.json. The caller is
// responsible for triggering a config rebuild + engine reload afterwards.
func (s *Store) SetAutoMode(on bool) error {
	pf, err := s.Read()
	if err != nil {
		return err
	}
	pf.Auto = on
	return s.Write(pf)
}

// Rename sets a new human label on an existing profile.
func (s *Store) Rename(name, newLabel string) error {
	newLabel = strings.TrimSpace(newLabel)
	if newLabel == "" {
		return fmt.Errorf("название не может быть пустым")
	}
	pf, err := s.Read()
	if err != nil {
		return err
	}
	for i := range pf.Profiles {
		if pf.Profiles[i].Name == name {
			pf.Profiles[i].Label = newLabel
			return s.Write(pf)
		}
	}
	return fmt.Errorf("профиль %q не найден", name)
}

// Delete removes a profile by name. Refuses to delete the last one or the
// currently active one (caller must switch first).
func (s *Store) Delete(name string) error {
	pf, err := s.Read()
	if err != nil {
		return err
	}
	if len(pf.Profiles) <= 1 {
		return fmt.Errorf("нельзя удалить единственный профиль")
	}
	if pf.Active == name {
		return fmt.Errorf("сначала переключитесь на другой профиль")
	}
	out := pf.Profiles[:0]
	found := false
	for _, p := range pf.Profiles {
		if p.Name == name {
			found = true
			continue
		}
		out = append(out, p)
	}
	if !found {
		return fmt.Errorf("профиль %q не найден", name)
	}
	pf.Profiles = out
	return s.Write(pf)
}

// Add appends a new profile with a unique name derived from label.
func (s *Store) Add(label, kind string, cfg map[string]interface{}) (string, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		return "", fmt.Errorf("название не может быть пустым")
	}
	if cfg == nil {
		return "", fmt.Errorf("конфигурация пустая")
	}
	pf, err := s.Read()
	if err != nil {
		return "", err
	}
	name := uniqueName(pf, label)
	pf.Profiles = append(pf.Profiles, Profile{
		Name:   name,
		Label:  label,
		Kind:   kind,
		Config: cfg,
	})
	if err := s.Write(pf); err != nil {
		return "", err
	}
	return name, nil
}

// Snapshot returns the per-profile specs the config rebuild needs, the
// active profile's tag, and whether auto-fastest mode is on. Tags are
// forced onto each Config map, and Configs are cloned so callers can
// mutate freely.
func (s *Store) Snapshot() ([]Spec, string, bool, error) {
	pf, err := s.Read()
	if err != nil {
		return nil, "", false, err
	}
	specs := make([]Spec, 0, len(pf.Profiles))
	activeTag := ""
	for _, p := range pf.Profiles {
		if p.Config == nil {
			continue
		}
		kind := p.Kind
		if kind == "" {
			kind = KindEndpoint
		}
		tag := ProfileTag(p.Name)
		cfg := cloneMap(p.Config)
		cfg["tag"] = tag
		specs = append(specs, Spec{Tag: tag, Kind: kind, Config: cfg, Name: p.Name})
		if p.Name == pf.Active {
			activeTag = tag
		}
	}
	if activeTag == "" && len(specs) > 0 {
		activeTag = specs[0].Tag
	}
	return specs, activeTag, pf.Auto, nil
}

// SetActive marks the named profile active after config.json was updated.
func (s *Store) SetActive(name string) error {
	pf, err := s.Read()
	if err != nil {
		return err
	}
	for _, p := range pf.Profiles {
		if p.Name == name {
			pf.Active = name
			return s.Write(pf)
		}
	}
	return fmt.Errorf("профиль не найден: %s", name)
}

// PeerHostPort returns (host, port) for a profile — used by the TCP-fallback
// probe when the engine isn't up. Returns (host, 0) when the port can't be
// determined from the profile config.
func PeerHostPort(p Profile) (string, int) {
	if p.Config == nil {
		return "", 0
	}
	if p.Kind == KindOutbound {
		host, _ := p.Config["server"].(string)
		switch v := p.Config["server_port"].(type) {
		case float64:
			return host, int(v)
		case int:
			return host, v
		}
		return host, 0
	}
	peers, ok := p.Config["peers"].([]interface{})
	if !ok || len(peers) == 0 {
		return "", 0
	}
	p0, ok := peers[0].(map[string]interface{})
	if !ok {
		return "", 0
	}
	host, _ := p0["address"].(string)
	switch v := p0["port"].(type) {
	case float64:
		return host, int(v)
	case int:
		return host, v
	}
	return host, 0
}

// PeerHost returns the server host/IP for a profile. Works for both endpoint
// (peers[0].address) and outbound (server) shapes.
func PeerHost(p Profile) string {
	if p.Config == nil {
		return ""
	}
	if p.Kind == KindOutbound {
		if s, _ := p.Config["server"].(string); s != "" {
			return s
		}
		return ""
	}
	return endpointPeerHost(p.Config)
}

func endpointPeerHost(ep map[string]interface{}) string {
	peers, ok := ep["peers"].([]interface{})
	if !ok || len(peers) == 0 {
		return ""
	}
	p0, ok := peers[0].(map[string]interface{})
	if !ok {
		return ""
	}
	if s, _ := p0["address"].(string); s != "" {
		return s
	}
	return ""
}

func peerEndpointForProfile(p Profile) string {
	if p.Config == nil {
		return ""
	}
	if p.Kind == KindOutbound {
		host, _ := p.Config["server"].(string)
		if host == "" {
			return ""
		}
		switch v := p.Config["server_port"].(type) {
		case float64:
			return fmt.Sprintf("%s:%d", host, int(v))
		case int:
			return fmt.Sprintf("%s:%d", host, v)
		default:
			return host
		}
	}
	return endpointPeerString(p.Config)
}

func endpointPeerString(ep map[string]interface{}) string {
	peers, ok := ep["peers"].([]interface{})
	if !ok || len(peers) == 0 {
		return ""
	}
	p0, ok := peers[0].(map[string]interface{})
	if !ok {
		return ""
	}
	host, _ := p0["address"].(string)
	if host == "" {
		return ""
	}
	switch v := p0["port"].(type) {
	case float64:
		return fmt.Sprintf("%s:%d", host, int(v))
	case int:
		return fmt.Sprintf("%s:%d", host, v)
	default:
		return host
	}
}

func prettyProtocol(p Profile) string {
	if p.Config == nil {
		return "—"
	}
	t, _ := p.Config["type"].(string)
	switch strings.ToLower(t) {
	case "awg":
		return "AmneziaWG"
	case "wireguard":
		return "WireGuard"
	case "hysteria2":
		return "Hysteria2"
	case "vless":
		return "VLESS"
	case "shadowsocks":
		return "Shadowsocks"
	case "trojan":
		return "Trojan"
	case "":
		return "—"
	default:
		return strings.ToUpper(t)
	}
}

func cloneMap(m map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func uniqueName(pf *File, label string) string {
	base := slugify(label)
	if base == "" {
		base = "profile"
	}
	taken := make(map[string]bool, len(pf.Profiles))
	for _, p := range pf.Profiles {
		taken[p.Name] = true
	}
	if !taken[base] {
		return base
	}
	for i := 2; ; i++ {
		n := fmt.Sprintf("%s-%d", base, i)
		if !taken[n] {
			return n
		}
	}
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		case r == ' ' || r == '-' || r == '_':
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
