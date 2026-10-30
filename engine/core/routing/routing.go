package routing

import (
	"NekkoVPN/core/fsutil"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Store persists the client's routing policy independently of any engine.
type Store struct {
	path   string
	mu     sync.Mutex
	memory *RoutingState
}

func New(path string) *Store { return &Store{path: path} }

func NewMemory() *Store {
	state := DefaultRoutingState()
	return &Store{memory: &state}
}

func (state RoutingState) Clone() RoutingState {
	clone := func(list RouteList) RouteList {
		return RouteList{Domains: append([]string{}, list.Domains...), IPs: append([]string{}, list.IPs...), Processes: append([]string{}, list.Processes...), Folders: append([]string{}, list.Folders...)}
	}
	state.Bypass, state.Proxy = clone(state.Bypass), clone(state.Proxy)
	return state
}

// ChangeRule is the common command operation used before persistence.
func (state *RoutingState) ChangeRule(mode RoutingMode, category, value string, remove bool) error {
	normalized, err := NormalizeValue(category, value)
	if err != nil {
		return err
	}
	list, err := pickList(state, mode)
	if err != nil {
		return err
	}
	if remove {
		return removeFromList(list, category, normalized)
	}
	return addToList(list, category, normalized)
}

// RoutingMode is what the list in RoutingState means:
//
//   - ModeBypass — entries in the list go DIRECT, everything else through VPN.
//   - ModeProxy  — entries in the list go through VPN, everything else DIRECT.
type RoutingMode string

const (
	ModeBypass RoutingMode = "bypass"
	ModeProxy  RoutingMode = "proxy"
)

// RouteList holds the category lists the UI exposes per mode. Folders are
// path fragments (see normalizeFolderValue); omitted when empty so files
// written before folder rules existed keep reading the same.
type RouteList struct {
	Domains   []string `json:"domains"`
	IPs       []string `json:"ips"`
	Processes []string `json:"processes"`
	Folders   []string `json:"folders,omitempty"`
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
		Bypass:  RouteList{Domains: []string{}, IPs: []string{}, Processes: []string{}, Folders: []string{}},
		Proxy:   RouteList{Domains: []string{}, IPs: []string{}, Processes: []string{}, Folders: []string{}},
	}
}

// sanitize normalises a freshly-decoded RoutingState: nil slices become
// empty, unknown modes fall back to bypass. Safe to call on Default too.
func (s RoutingState) Sanitize() RoutingState {
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
	l.Folders = keepValid("folders", l.Folders)
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.memory != nil {
		return s.memory.Clone(), nil
	}
	raw, err := os.ReadFile(s.path)
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
	return state.Sanitize(), nil
}

// WriteRouting persists routing.json. Caller usually follows with
// applyRoutingToConfig so the live sing-box config matches.
func (s *Store) WriteRouting(state RoutingState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.memory != nil {
		next := state.Sanitize().Clone()
		s.memory = &next
		return nil
	}
	out, err := json.MarshalIndent(state.Sanitize(), "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(s.path), ".routing-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(out)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return fsutil.Replace(file.Name(), s.path)
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
	return nil
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
	return nil
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
	return nil
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
	return nil
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
	case "folders":
		if !containsCI(list.Folders, value) {
			list.Folders = append(list.Folders, value)
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
	case "folders":
		list.Folders = drop(list.Folders)
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
