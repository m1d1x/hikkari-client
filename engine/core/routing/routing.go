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
	state.Bypass, state.Proxy = state.Bypass.clone(), state.Proxy.clone()
	return state
}

func (list RouteList) clone() RouteList {
	out := RouteList{Domains: append([]string{}, list.Domains...), IPs: append([]string{}, list.IPs...), Processes: append([]string{}, list.Processes...), Folders: append([]string{}, list.Folders...)}
	if list.Off != nil {
		off := list.Off.clone()
		out.Off = &off
	}
	return out
}

// Active is the policy that is routed: each list without the rules the person
// switched off. Everything that builds an engine config reads this, never
// the stored lists directly.
func (state RoutingState) Active() RoutingState {
	state.Bypass, state.Proxy = state.Bypass.active(), state.Proxy.active()
	return state
}

func (list RouteList) active() RouteList {
	out := list.clone()
	if list.Off == nil {
		return out
	}
	keep := func(values, off []string) []string {
		kept := make([]string, 0, len(values))
		for _, v := range values {
			if !containsCI(off, v) {
				kept = append(kept, v)
			}
		}
		return kept
	}
	out.Domains = keep(list.Domains, list.Off.Domains)
	out.IPs = keep(list.IPs, list.Off.IPs)
	out.Processes = keep(list.Processes, list.Off.Processes)
	out.Folders = keep(list.Folders, list.Off.Folders)
	out.Off = nil
	return out
}

// SetRuleOn switches a stored rule off or back on without removing it: the
// rule keeps its place in the list and comes back exactly as it was.
func (state *RoutingState) SetRuleOn(mode RoutingMode, category, value string, on bool) error {
	normalized, err := NormalizeValue(category, value)
	if err != nil {
		return err
	}
	list, err := pickList(state, mode)
	if err != nil {
		return err
	}
	values, err := categoryValues(list, category)
	if err != nil {
		return err
	}
	if !containsCI(values, normalized) {
		return fmt.Errorf("правила %q нет в списке", value)
	}
	if on {
		if list.Off != nil {
			if err := removeFromList(list.Off, category, normalized); err != nil {
				return err
			}
		}
	} else {
		if list.Off == nil {
			list.Off = &RouteList{}
		}
		if err := addToList(list.Off, category, normalized); err != nil {
			return err
		}
	}
	list.compactOff()
	return nil
}

func categoryValues(list *RouteList, category string) ([]string, error) {
	switch category {
	case "domains":
		return list.Domains, nil
	case "ips":
		return list.IPs, nil
	case "processes":
		return list.Processes, nil
	case "folders":
		return list.Folders, nil
	}
	return nil, fmt.Errorf("неизвестная категория: %s", category)
}

// compactOff keeps Off a subset of the list it belongs to, and nil when empty,
// so a file without switched-off rules looks exactly as it did before.
func (list *RouteList) compactOff() {
	if list.Off == nil {
		return
	}
	sub := func(off, values []string) []string {
		out := []string{}
		for _, v := range off {
			if containsCI(values, v) && !containsCI(out, v) {
				out = append(out, v)
			}
		}
		return out
	}
	off := RouteList{
		Domains:   sub(list.Off.Domains, list.Domains),
		IPs:       sub(list.Off.IPs, list.IPs),
		Processes: sub(list.Off.Processes, list.Processes),
		Folders:   sub(list.Off.Folders, list.Folders),
	}
	if len(off.Domains)+len(off.IPs)+len(off.Processes)+len(off.Folders) == 0 {
		list.Off = nil
		return
	}
	list.Off = &off
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
		err = removeFromList(list, category, normalized)
	} else {
		// Adding a rule that is there but switched off switches it back on:
		// the person asked for it to work.
		if list.Off != nil {
			err = removeFromList(list.Off, category, normalized)
		}
		if err == nil {
			err = addToList(list, category, normalized)
		}
	}
	list.compactOff()
	return err
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
	// Off holds the entries of this list the person switched off: they stay
	// in the list, in their place, and are not routed (see Active). Always a
	// subset of the list, nil when nothing is off; never has an Off of its own.
	Off *RouteList `json:"off,omitempty"`
}

// RoutingState is the authoritative split-routing config. Persisted to
// routing.json next to config.json; the sing-box route.rules are computed
// from this every time we apply.
type RoutingState struct {
	Enabled bool        `json:"enabled"`
	Mode    RoutingMode `json:"mode"`
	// ModeChosen is set once the person picks the mode themselves (the
	// switch, or an imported snapshot that names one). Until then Mode
	// follows the default the catalog from the hub gives; after it the
	// person's pick wins over the hub.
	ModeChosen bool      `json:"modeChosen,omitempty"`
	Bypass     RouteList `json:"bypass"`
	Proxy      RouteList `json:"proxy"`
}

// FollowDefault puts a mode the person never picked on def, the default the
// hub's catalog gives; "" is a catalog without one, and means the client's
// own default, bypass. A picked mode is returned as it is.
func (s RoutingState) FollowDefault(def RoutingMode) RoutingState {
	if s.ModeChosen {
		return s
	}
	if def != ModeProxy {
		def = ModeBypass
	}
	s.Mode = def
	return s
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
	if l.Off != nil {
		off := RouteList{
			Domains:   keepValid("domains", l.Off.Domains),
			IPs:       keepValid("ips", l.Off.IPs),
			Processes: keepValid("processes", l.Off.Processes),
			Folders:   keepValid("folders", l.Off.Folders),
		}
		l.Off = &off
		l.compactOff()
	}
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
	state.Mode, state.ModeChosen = mode, true
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
