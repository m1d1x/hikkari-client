// Package profile manages profiles.json — the list of VPN servers the user
// can switch between. Switching a profile only swaps cfg["endpoints"] in the
// shared config.json; route/dns/whitelist rules stay shared across servers.
package profile

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// SharedEndpointTag is forced onto every active endpoint so route.final and
// dns.detour references in config.json keep matching after a profile switch.
const SharedEndpointTag = "awg-ep"

// Profile is one server entry stored in profiles.json.
type Profile struct {
	Name     string                 `json:"name"`
	Label    string                 `json:"label"`
	Endpoint map[string]interface{} `json:"endpoint"`
}

// File is the on-disk shape of profiles.json.
type File struct {
	Active   string    `json:"active"`
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

func (s *Store) Read() (*File, error) {
	raw, err := os.ReadFile(s.path)
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
		Active:   "main",
		Profiles: []Profile{{Name: "main", Label: "Основной", Endpoint: ep}},
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
			Endpoint: PeerEndpoint(p.Endpoint),
			Protocol: prettyProtocol(p.Endpoint),
		})
	}
	return out, nil
}

func (s *Store) Active() (string, error) {
	pf, err := s.Read()
	if err != nil {
		return "", err
	}
	return pf.Active, nil
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

// PrepareSwitch returns the endpoints array to write into config.json for
// the named profile. The caller is responsible for writing the endpoints,
// marking the profile active, and restarting the core. Returns the endpoint
// map (with the shared tag set) so callers can pass it straight into
// Store.SetEndpoints.
func (s *Store) PrepareSwitch(name string) ([]interface{}, error) {
	pf, err := s.Read()
	if err != nil {
		return nil, err
	}
	var target *Profile
	for i := range pf.Profiles {
		if pf.Profiles[i].Name == name {
			target = &pf.Profiles[i]
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("профиль не найден: %s", name)
	}
	if target.Endpoint == nil {
		return nil, fmt.Errorf("у профиля %q нет данных сервера", name)
	}
	ep := target.Endpoint
	ep["tag"] = SharedEndpointTag
	return []interface{}{ep}, nil
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

// PeerHost extracts peer[0].address (the server IP/host) from an endpoint map.
func PeerHost(ep map[string]interface{}) string {
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

// PeerEndpoint formats "host:port" out of an AWG endpoint map.
func PeerEndpoint(ep map[string]interface{}) string {
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

func prettyProtocol(ep map[string]interface{}) string {
	t, _ := ep["type"].(string)
	switch strings.ToLower(t) {
	case "awg":
		return "AmneziaWG"
	case "wireguard":
		return "WireGuard"
	case "":
		return "—"
	default:
		return strings.ToUpper(t)
	}
}
