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
	"strings"
)

// Whitelist holds the three "direct" (bypass-VPN) lists shown in the UI.
type Whitelist struct {
	Domains   []string `json:"domains"`
	IPs       []string `json:"ips"`
	Processes []string `json:"processes"`
}

// Store is a thin file-backed config repository pinned to a single path.
type Store struct {
	path string
}

func New(path string) *Store { return &Store{path: path} }

func (s *Store) Path() string { return s.path }

// ruleKeyByCategory maps the UI category name to the sing-box rule key.
var ruleKeyByCategory = map[string]string{
	"domains":   "domain_suffix",
	"ips":       "ip_cidr",
	"processes": "process_name",
}

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

// GetWhitelist reads the three direct lists out of config.json.
func (s *Store) GetWhitelist() (Whitelist, error) {
	cfg, err := s.Read()
	if err != nil {
		return Whitelist{}, err
	}
	wl := Whitelist{Domains: []string{}, IPs: []string{}, Processes: []string{}}
	for _, rule := range directRules(cfg) {
		wl.Domains = appendStrings(wl.Domains, rule["domain_suffix"])
		wl.IPs = appendStrings(wl.IPs, rule["ip_cidr"])
		wl.Processes = appendStrings(wl.Processes, rule["process_name"])
	}
	return wl, nil
}

// AddRule adds a value to a whitelist category (domains|ips|processes) and saves.
func (s *Store) AddRule(category, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("пустое значение")
	}
	key, ok := ruleKeyByCategory[category]
	if !ok {
		return fmt.Errorf("неизвестная категория: %s", category)
	}
	cfg, err := s.Read()
	if err != nil {
		return err
	}
	rule := findOrCreateDirectRule(cfg, key)
	list := toStringSlice(rule[key])
	for _, v := range list {
		if strings.EqualFold(v, value) {
			return nil
		}
	}
	rule[key] = append(list, value)
	return s.Write(cfg)
}

// RemoveRule removes a value from a whitelist category and saves.
func (s *Store) RemoveRule(category, value string) error {
	key, ok := ruleKeyByCategory[category]
	if !ok {
		return fmt.Errorf("неизвестная категория: %s", category)
	}
	cfg, err := s.Read()
	if err != nil {
		return err
	}
	for _, rule := range directRules(cfg) {
		if _, has := rule[key]; !has {
			continue
		}
		list := toStringSlice(rule[key])
		out := list[:0]
		for _, v := range list {
			if !strings.EqualFold(v, value) {
				out = append(out, v)
			}
		}
		rule[key] = out
	}
	return s.Write(cfg)
}

// SetEndpoints replaces cfg["endpoints"] with a single endpoint, used by
// the profile-switch flow.
func (s *Store) SetEndpoints(endpoints []interface{}) error {
	cfg, err := s.Read()
	if err != nil {
		return err
	}
	cfg["endpoints"] = endpoints
	return s.Write(cfg)
}

// directRules returns route.rules entries that route to outbound "direct".
func directRules(cfg map[string]interface{}) []map[string]interface{} {
	var out []map[string]interface{}
	route, _ := cfg["route"].(map[string]interface{})
	if route == nil {
		return out
	}
	rules, _ := route["rules"].([]interface{})
	for _, r := range rules {
		rule, _ := r.(map[string]interface{})
		if rule == nil {
			continue
		}
		if ob, _ := rule["outbound"].(string); ob == "direct" {
			out = append(out, rule)
		}
	}
	return out
}

// findOrCreateDirectRule returns the direct rule holding `key`, creating one if needed.
func findOrCreateDirectRule(cfg map[string]interface{}, key string) map[string]interface{} {
	for _, rule := range directRules(cfg) {
		if _, has := rule[key]; has {
			return rule
		}
	}
	route, _ := cfg["route"].(map[string]interface{})
	if route == nil {
		route = map[string]interface{}{}
		cfg["route"] = route
	}
	rules, _ := route["rules"].([]interface{})
	newRule := map[string]interface{}{key: []interface{}{}, "outbound": "direct"}
	route["rules"] = append(rules, newRule)
	return newRule
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

func toStringSlice(v interface{}) []string {
	arr, _ := v.([]interface{})
	out := make([]string, 0, len(arr))
	for _, it := range arr {
		if s, ok := it.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func appendStrings(dst []string, v interface{}) []string {
	return append(dst, toStringSlice(v)...)
}
