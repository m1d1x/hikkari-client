package svcproto

import (
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"
)

// SanitizeXray is Sanitize for the Xray core: it turns an Xray config written
// by an unprivileged process into one the service may run as SYSTEM/root, and
// returns the address system DNS must point at while it runs (the TUN
// gateway, where the config answers DNS).
//
// The threat is the same as for sing-box — the file system, not the network.
// Xray opens files named in its config (`log.access`/`log.error` written
// anywhere, `certificateFile`/`keyFile` read from anywhere, a TLS
// `masterKeyLog`, `ext:file:tag` geodata), listens on unix sockets named by
// path, and serves an API or metrics when asked. So:
//
//   - top-level sections are allow-listed; anything else is refused;
//   - the log goes to stdout, which the service pipes into its own log;
//   - inbounds are the TUN device and SOCKS on loopback, nothing else;
//   - outbounds are the protocols the GUI compiles to, nothing else;
//   - any key naming a file, and any value naming a socket path or external
//     geodata, anywhere, is refused — the GUI never generates one.
//
// As with sing-box, refusing beats silently stripping.
func SanitizeXray(raw []byte) (cfg []byte, dns string, err error) {
	var c map[string]interface{}
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, "", fmt.Errorf("config is not a JSON object: %w", err)
	}
	if c == nil {
		return nil, "", fmt.Errorf("config is empty")
	}
	for key := range c {
		if !allowedXrayTopLevel[key] {
			return nil, "", fmt.Errorf("config section %q is not allowed", key)
		}
	}
	if err := refuseXrayFiles(c, ""); err != nil {
		return nil, "", err
	}

	if l, ok := c["log"].(map[string]interface{}); ok {
		for key := range l {
			if key != "loglevel" && key != "dnsLog" {
				return nil, "", fmt.Errorf("log.%s is not allowed", key)
			}
		}
	}

	inbounds, _ := c["inbounds"].([]interface{})
	for i, v := range inbounds {
		in, ok := v.(map[string]interface{})
		if !ok {
			return nil, "", fmt.Errorf("inbounds[%d] is not an object", i)
		}
		switch in["protocol"] {
		case "tun":
			if dns != "" {
				return nil, "", fmt.Errorf("inbounds[%d]: only one TUN inbound is allowed", i)
			}
			if dns, err = tunGateway(in); err != nil {
				return nil, "", fmt.Errorf("inbounds[%d]: %w", i, err)
			}
		case "socks":
			if listen, _ := in["listen"].(string); !isLoopbackIP(listen) {
				return nil, "", fmt.Errorf("inbounds[%d]: SOCKS must listen on loopback, not %q", i, listen)
			}
		default:
			return nil, "", fmt.Errorf("inbounds[%d]: protocol %v is not allowed", i, in["protocol"])
		}
	}
	if dns == "" {
		// Without the TUN inbound there is nothing for the service to do.
		return nil, "", fmt.Errorf("config has no TUN inbound")
	}

	outbounds, _ := c["outbounds"].([]interface{})
	for i, v := range outbounds {
		out, ok := v.(map[string]interface{})
		if !ok {
			return nil, "", fmt.Errorf("outbounds[%d] is not an object", i)
		}
		if p, _ := out["protocol"].(string); !allowedXrayOutbounds[p] {
			return nil, "", fmt.Errorf("outbounds[%d]: protocol %q is not allowed", i, p)
		}
	}

	out, err := json.MarshalIndent(c, "", "  ")
	return out, dns, err
}

var allowedXrayTopLevel = map[string]bool{
	"log":         true,
	"dns":         true,
	"inbounds":    true,
	"outbounds":   true,
	"routing":     true,
	"observatory": true,
}

var allowedXrayOutbounds = map[string]bool{
	"blackhole":   true,
	"freedom":     true,
	"dns":         true,
	"vless":       true,
	"trojan":      true,
	"shadowsocks": true,
	"hysteria":    true,
}

// tunGateway is the first address of the TUN inbound's gateway: the one the
// config answers DNS on.
func tunGateway(in map[string]interface{}) (string, error) {
	settings, _ := in["settings"].(map[string]interface{})
	gateways, _ := settings["gateway"].([]interface{})
	if len(gateways) == 0 {
		return "", fmt.Errorf("TUN inbound has no gateway")
	}
	cidr, _ := gateways[0].(string)
	ip, _, err := net.ParseCIDR(cidr)
	if err != nil || ip.To4() == nil {
		return "", fmt.Errorf("TUN gateway %q is not an IPv4 network", cidr)
	}
	return ip.String(), nil
}

// refuseXrayFiles walks the whole tree for keys that name a file and for
// values that make Xray open one. `path` is not such a key: transports use it
// for the HTTP path ("/ws").
func refuseXrayFiles(v interface{}, at string) error {
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
			if isXrayFileKey(k) {
				return fmt.Errorf("%s names a file, which a service-run config may not", here)
			}
			if s, ok := t[k].(string); ok && (k == "listen" || k == "address") && isSocketPath(s) {
				return fmt.Errorf("%s names a unix socket, which a service-run config may not", here)
			}
			if err := refuseXrayFiles(t[k], here); err != nil {
				return err
			}
		}
	case []interface{}:
		for i, e := range t {
			if err := refuseXrayFiles(e, fmt.Sprintf("%s[%d]", at, i)); err != nil {
				return err
			}
		}
	case string:
		// ext:file.dat:tag reads geodata from a file the config names.
		if strings.HasPrefix(t, "ext:") || strings.HasPrefix(t, "ext-domain:") || strings.HasPrefix(t, "ext-ip:") {
			return fmt.Errorf("%s refers to external geodata, which a service-run config may not", at)
		}
	}
	return nil
}

func isXrayFileKey(k string) bool {
	return strings.HasSuffix(k, "File") || strings.HasSuffix(k, "_path") ||
		k == "masterKeyLog" || k == "access" || k == "error"
}

func isSocketPath(s string) bool {
	return strings.HasPrefix(s, "/") || strings.HasPrefix(s, "@")
}

func isLoopbackIP(s string) bool {
	ip := net.ParseIP(s)
	return ip != nil && ip.IsLoopback()
}
