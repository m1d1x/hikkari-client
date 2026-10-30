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
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Kind tells SwitchProfile which sing-box section to write the active profile
// into. "endpoint" → cfg["endpoints"][0]; "outbound" → append/replace in
// cfg["outbounds"] keeping the system outbounds (direct/block) in place.
const (
	KindEndpoint = "endpoint"
	KindOutbound = "outbound"
)

// Profile is one server entry stored in profiles.json. Config is the
// sing-box-shaped block (endpoint object for KindEndpoint, outbound object
// for KindOutbound).
// Connection is a portable protocol specification. URI is a standard server
// link or a WireGuard INI, never a sing-box/Xray configuration object.
type Connection struct {
	URI      string `json:"uri"`
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
}

// SubscriptionInfo is shared provider metadata copied onto each server from
// one imported response. ID is an opaque local grouping key, never the URL.
type SubscriptionInfo struct {
	ID                    string `json:"id"`
	Title                 string `json:"title,omitempty"`
	UploadBytes           int64  `json:"uploadBytes,omitempty"`
	DownloadBytes         int64  `json:"downloadBytes,omitempty"`
	TotalBytes            int64  `json:"totalBytes,omitempty"`
	ExpiresAt             int64  `json:"expiresAt,omitempty"`
	UpdateIntervalSeconds int64  `json:"updateIntervalSeconds,omitempty"`
	WebPageURL            string `json:"webPageUrl,omitempty"`
	SupportURL            string `json:"supportUrl,omitempty"`
}

type Profile struct {
	Name         string            `json:"name"`
	Label        string            `json:"label"`
	Kind         string            `json:"kind,omitempty"`
	Connection   *Connection       `json:"connection,omitempty"`
	Subscription *SubscriptionInfo `json:"subscription,omitempty"`
	// MetadataOnly marks provider sentinel entries such as an encoded
	// "remaining days" URI. It is consumed during import and never persisted.
	MetadataOnly  bool   `json:"-"`
	ProviderLabel string `json:"providerLabel,omitempty"`
	// Kind/Config are retained only for lossless migration of legacy installs.
	Config map[string]interface{} `json:"config,omitempty"`
}

// UnmarshalJSON tolerates the legacy on-disk shape that used "endpoint"
// instead of "config" and had no "kind" field at all. Anything legacy is
// treated as KindEndpoint.
func (p *Profile) UnmarshalJSON(data []byte) error {
	var r struct {
		ProviderLabel string                 `json:"providerLabel"`
		Connection    *Connection            `json:"connection"`
		Subscription  *SubscriptionInfo      `json:"subscription"`
		Name          string                 `json:"name"`
		Label         string                 `json:"label"`
		Kind          string                 `json:"kind"`
		Config        map[string]interface{} `json:"config"`
		Endpoint      map[string]interface{} `json:"endpoint"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	p.ProviderLabel = r.ProviderLabel
	p.Connection = r.Connection
	p.Subscription = r.Subscription
	p.Name = r.Name
	p.Label = r.Label
	p.Kind = r.Kind
	p.Config = r.Config
	if p.Config == nil && r.Endpoint != nil {
		p.Config = r.Endpoint
	}
	if p.Kind == "" && p.Connection == nil {
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
	Auto          bool           `json:"auto,omitempty"`
	Profiles      []Profile      `json:"profiles"`
	Subscriptions []Subscription `json:"subscriptions,omitempty"`
}

// Info is the lightweight projection the UI renders.
type Info struct {
	Name         string            `json:"name"`
	Label        string            `json:"label"`
	Active       bool              `json:"active"`
	Endpoint     string            `json:"endpoint"`
	Protocol     string            `json:"protocol"`
	Subscription *SubscriptionInfo `json:"subscription,omitempty"`
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

// PendingWrite prepares an owner-only, synced snapshot without publishing it.
// Runtime owners can publish only after the candidate configuration has started.
type PendingWrite struct {
	temporary   string
	destination string
}

func (w *PendingWrite) Commit() error { return os.Rename(w.temporary, w.destination) }
func (w *PendingWrite) Discard()      { _ = os.Remove(w.temporary) }

func (s *Store) Stage(pf *File) (*PendingWrite, error) {
	out, err := json.MarshalIndent(pf, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".profiles-*")
	if err != nil {
		return nil, err
	}
	pending := &PendingWrite{temporary: f.Name(), destination: s.path}
	fail := func(err error) (*PendingWrite, error) { _ = f.Close(); pending.Discard(); return nil, err }
	if _, err := f.Write(out); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		pending.Discard()
		return nil, err
	}
	return pending, nil
}
func (s *Store) Write(pf *File) error {
	pending, err := s.Stage(pf)
	if err != nil {
		return err
	}
	defer pending.Discard()
	return pending.Commit()
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
			Name:         p.Name,
			Label:        displayLabel(p),
			Active:       p.Name == pf.Active,
			Endpoint:     peerEndpointForProfile(p),
			Protocol:     prettyProtocol(p),
			Subscription: p.Subscription,
		})
	}
	return out, nil
}

// displayLabel keeps stored IDs stable while cleaning labels from older imports.
func displayLabel(p Profile) string {
	if label := CleanLabel(p.Label); label != "" {
		return label
	}
	if p.Connection != nil && p.Connection.Host != "" {
		return p.Connection.Host
	}
	return "Профиль"
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

// Delete removes any profile. Deleting the active one selects the first
// remaining profile; deleting the last one clears the active selection.
func (s *Store) Delete(name string) error {
	pf, err := s.Read()
	if err != nil {
		return err
	}
	hidden := map[string]ServerIdentity{}
	for _, p := range pf.Profiles {
		if p.Name != name || p.Subscription == nil {
			continue
		}
		id := Identity(p)
		for _, other := range pf.Profiles {
			if other.Name == p.Name || other.Subscription == nil || other.Subscription.ID != p.Subscription.ID {
				continue
			}
			otherID := Identity(other)
			if otherID.Label == id.Label {
				id.Label = ""
			}
			if otherID.Endpoint == id.Endpoint {
				id.Endpoint = ""
			}
		}
		hidden[p.Name] = id
	}
	out := pf.Profiles[:0]
	found := false
	for _, p := range pf.Profiles {
		if p.Name == name {
			found = true
			for i := range pf.Subscriptions {
				if p.Subscription != nil && pf.Subscriptions[i].ID == p.Subscription.ID {
					pf.Subscriptions[i].Hidden = append(pf.Subscriptions[i].Hidden, hidden[p.Name])
				}
			}
			continue
		}
		out = append(out, p)
	}
	if !found {
		return fmt.Errorf("профиль %q не найден", name)
	}
	pf.Profiles = out
	if len(out) == 0 {
		pf.Active = ""
		pf.Auto = false
	} else if pf.Active == name {
		pf.Active = out[0].Name
	}
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

// AddConnection appends a portable profile without persisting native config.
func (s *Store) AddConnection(p Profile) (string, error) {
	names, err := s.AddConnections([]Profile{p})
	if err != nil {
		return "", err
	}
	return names[0], nil
}

// AddConnections appends a subscription's servers in one profiles.json
// transaction so an interrupted write cannot leave a partially imported group.
func (s *Store) AddConnections(profiles []Profile) ([]string, error) {
	pf, err := s.Read()
	if err != nil {
		return nil, err
	}
	names, err := AppendConnections(pf, profiles)
	if err != nil {
		return nil, err
	}
	if err := s.Write(pf); err != nil {
		return nil, err
	}
	return names, nil
}

// AppendConnections stages standalone imports without an intermediate disk write.
func AppendConnections(pf *File, profiles []Profile) ([]string, error) {
	if len(profiles) == 0 {
		return nil, fmt.Errorf("пустая конфигурация подключения")
	}
	names := make([]string, 0, len(profiles))
	for _, p := range profiles {
		if p.MetadataOnly {
			if p.Connection != nil && p.Connection.URI != "" {
				kept := pf.Profiles[:0]
				for _, existing := range pf.Profiles {
					if existing.Connection != nil && existing.Connection.URI == p.Connection.URI {
						if pf.Active == existing.Name {
							pf.Active = ""
						}
						continue
					}
					kept = append(kept, existing)
				}
				pf.Profiles = kept
				if pf.Active == "" && len(pf.Profiles) > 0 {
					pf.Active = pf.Profiles[0].Name
				}
			}
			continue
		}
		if p.Connection == nil || p.Connection.URI == "" {
			return nil, fmt.Errorf("пустая конфигурация подключения")
		}
		p.Label = CleanLabel(p.Label)
		if p.Label == "" {
			p.Label = strings.TrimSpace(p.Connection.Host)
		}
		if p.Label == "" {
			return nil, fmt.Errorf("название не может быть пустым")
		}
		if p.Subscription != nil {
			matched := false
			for i := range pf.Profiles {
				existing := &pf.Profiles[i]
				if existing.Connection != nil && existing.Connection.URI == p.Connection.URI {
					existing.Subscription = p.Subscription
					names = append(names, existing.Name)
					matched = true
					break
				}
			}
			if matched {
				continue
			}
		}
		p.Name = uniqueName(pf, p.Label)
		p.Config = nil
		p.Kind = ""
		pf.Profiles = append(pf.Profiles, p)
		names = append(names, p.Name)
	}
	return names, nil
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
	if p.Connection != nil {
		return p.Connection.Host, p.Connection.Port
	}
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
	if p.Connection != nil {
		return p.Connection.Host
	}
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
	if p.Connection != nil {
		return net.JoinHostPort(p.Connection.Host, strconv.Itoa(p.Connection.Port))
	}
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
	if p.Connection != nil {
		switch p.Connection.Protocol {
		case "wireguard":
			return "WireGuard"
		case "awg":
			return "AmneziaWG"
		case "hysteria2":
			return "Hysteria2"
		case "shadowsocks":
			return "Shadowsocks"
		default:
			return strings.ToUpper(p.Connection.Protocol)
		}
	}
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
