package svcproto

import (
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"

	"NekkoVPN/core/autotunnel"
)

// Paths are the service-owned locations a sanitised config may refer to.
type Paths struct {
	// CacheFile is where sing-box keeps cache.db (fake-ip store, selections).
	CacheFile string
	// AutoTunnelRuleSet is the autotunnel host list (core/autotunnel) as the
	// service keeps it. The one local rule set a GUI config may declare.
	AutoTunnelRuleSet string
}

// Sanitize turns a config written by an unprivileged process into one the
// service may run as SYSTEM.
//
// The threat is not the network part — a user who can start the VPN may
// route their traffic however they like — but the file system: sing-box opens
// paths named in its config, and as SYSTEM any of them is a write or read
// anywhere on the machine (`log.output` onto a system DLL, `cache_file.path`
// over a service binary, `external_ui` serving C:\ over the Clash API, a
// `*_path` key reading another user's files). So:
//
//   - top-level sections are allow-listed; anything else is refused;
//   - log output and the cache file are forced to service-owned locations;
//   - the Clash API may only listen on loopback and may not serve files;
//   - the autotunnel rule set — the one local rule set the GUI compiles — is
//     rewritten to the service's own location, and its content arrives as data
//     over the control endpoint (OpAutoTunnel), never as a path;
//   - any other key naming a file or directory, anywhere, is refused — a
//     config the GUI generates never has one, so meeting one means the config
//     did not come from the GUI.
//
// Refusing beats silently stripping: a stripped config would start and
// behave differently from what the GUI shows.
func Sanitize(raw []byte, p Paths) ([]byte, error) {
	var cfg map[string]interface{}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("config is not a JSON object: %w", err)
	}
	if cfg == nil {
		return nil, fmt.Errorf("config is empty")
	}
	if _, ok := cfg["inbounds"]; !ok {
		// Without the tun inbound there is nothing for the service to do.
		return nil, fmt.Errorf("config has no inbounds")
	}

	for key := range cfg {
		if !allowedTopLevel[key] {
			return nil, fmt.Errorf("config section %q is not allowed", key)
		}
	}
	if err := refuseFileKeys(cfg, ""); err != nil {
		return nil, err
	}

	// Log: to stdout, which the service pipes into its own log.
	if l, ok := cfg["log"].(map[string]interface{}); ok {
		delete(l, "output")
	}

	if exp, ok := cfg["experimental"].(map[string]interface{}); ok {
		for key := range exp {
			if !allowedExperimental[key] {
				return nil, fmt.Errorf("experimental.%s is not allowed", key)
			}
		}
		if api, ok := exp["clash_api"].(map[string]interface{}); ok {
			for _, key := range []string{"external_ui", "external_ui_download_url", "external_ui_download_detour"} {
				if _, present := api[key]; present {
					return nil, fmt.Errorf("experimental.clash_api.%s is not allowed", key)
				}
			}
			if ctl, ok := api["external_controller"].(string); ok && !isLoopback(ctl) {
				return nil, fmt.Errorf("clash API must listen on loopback, not %q", ctl)
			}
		}
		if cache, ok := exp["cache_file"].(map[string]interface{}); ok {
			cache["path"] = p.CacheFile
		}
	}

	if route, ok := cfg["route"].(map[string]interface{}); ok {
		if err := normaliseAutoTunnel(route, p.AutoTunnelRuleSet); err != nil {
			return nil, err
		}
	}

	return json.MarshalIndent(cfg, "", "  ")
}

// normaliseAutoTunnel keeps the one local rule set the GUI compiles — the
// autotunnel host list — and makes it service-owned: the path the GUI named
// is never used, the service's own location is written in its place, and the
// entry is rebuilt to exactly the shape the contract knows. Any other rule
// set is refused: a remote one would be fetched by the core as SYSTEM, and a
// foreign local path is exactly what this boundary exists to stop.
func normaliseAutoTunnel(route map[string]interface{}, servicePath string) error {
	sets, ok := route["rule_set"].([]interface{})
	if !ok || len(sets) == 0 {
		return nil
	}
	if len(sets) > 1 {
		return fmt.Errorf("route.rule_set: %d entries, the GUI compiles one", len(sets))
	}
	m, ok := sets[0].(map[string]interface{})
	if !ok {
		return fmt.Errorf("route.rule_set[0]: not an object")
	}
	tag, _ := m["tag"].(string)
	typ, _ := m["type"].(string)
	switch {
	case typ == "remote" || m["url"] != nil:
		return fmt.Errorf("route.rule_set[0]: remote rule sets are not allowed")
	case servicePath == "":
		return fmt.Errorf("route.rule_set[0]: the service gave no rule set location")
	case tag != autotunnel.Tag || typ != "local":
		return fmt.Errorf("route.rule_set[0]: local rule sets are not allowed (only the %q one is)", autotunnel.Tag)
	}
	route["rule_set"] = []interface{}{map[string]interface{}{
		"tag": autotunnel.Tag, "type": "local", "format": "source", "path": servicePath,
	}}
	return nil
}

var allowedTopLevel = map[string]bool{
	"log":          true,
	"dns":          true,
	"ntp":          true,
	"inbounds":     true,
	"outbounds":    true,
	"endpoints":    true,
	"route":        true,
	"experimental": true,
}

var allowedExperimental = map[string]bool{
	"clash_api":  true,
	"cache_file": true,
}

// refuseFileKeys walks the whole tree for keys that name a file or a
// directory. `path` alone is NOT one of them: transports use it for the HTTP
// path ("/ws"); the two places where it is a file are handled explicitly
// above — cache_file is forced to a service-owned location, and the
// autotunnel rule set is rewritten to one.
func refuseFileKeys(v interface{}, at string) error {
	switch t := v.(type) {
	case map[string]interface{}:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys) // deterministic error messages
		for _, k := range keys {
			here := k
			if at != "" {
				here = at + "." + k
			}
			if isFileKey(k) {
				return fmt.Errorf("%s names a file or directory, which a service-run config may not", here)
			}
			if err := refuseFileKeys(t[k], here); err != nil {
				return err
			}
		}
	case []interface{}:
		for i, e := range t {
			if err := refuseFileKeys(e, fmt.Sprintf("%s[%d]", at, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

func isFileKey(k string) bool {
	return strings.HasSuffix(k, "_path") ||
		strings.HasSuffix(k, "directory") ||
		k == "external_ui"
}

func isLoopback(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
