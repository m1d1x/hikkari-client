package configfile

import (
	"fmt"
	"net"
	"strings"
)

// fakeIPNet is the fake-ip range the tunnel's DNS hands out. An address from
// it means the lookup went through our own tunnel and is useless for dialing
// the server.
var fakeIPNet = &net.IPNet{IP: net.IPv4(198, 18, 0, 0), Mask: net.CIDRMask(15, 32)}

// Resolver resolves a hostname to IPv4 addresses. net.LookupIP-shaped so the
// api layer can pass the system resolver and tests a stub.
type Resolver func(host string) ([]net.IP, error)

// PinResult reports what PinServerIPs did to one outbound.
type PinResult struct {
	Tag  string
	Host string
	IP   string
	Err  error
}

// PinServerIPs replaces each profile outbound's hostname "server" with an IP
// resolved by resolve, and writes config.json back.
//
// Why: the core resolves outbound servers through dns-direct ("local", the
// OS resolver). Once the TUN is up, the OS resolver's own queries enter the
// tunnel, so on some starts the server's name could not be resolved — or
// came back as a fake-ip — and every dial went to 198.18.x.x through the
// tunnel itself ("dial tcp 198.18.0.84:443: i/o timeout"). Resolving here,
// before sing-box starts and while the tunnel is down, removes DNS from the
// path to the server entirely.
//
// The hostname is kept where the wire format needs it: tls.server_name (SNI)
// and the transport's host header, so the server sees exactly what it saw
// before. A failed lookup or a fake-ip answer leaves the hostname in place.
func (s *Store) PinServerIPs(resolve Resolver) ([]PinResult, error) {
	cfg, err := s.Read()
	if err != nil {
		return nil, err
	}
	obs, _ := cfg["outbounds"].([]interface{})
	var results []PinResult
	for _, it := range obs {
		ob, ok := it.(map[string]interface{})
		if !ok {
			continue
		}
		host, _ := ob["server"].(string)
		host = strings.TrimSpace(host)
		if host == "" || net.ParseIP(host) != nil {
			continue
		}
		tag, _ := ob["tag"].(string)
		res := PinResult{Tag: tag, Host: host}
		ip, err := pickIPv4(resolve, host)
		if err != nil {
			res.Err = err
			results = append(results, res)
			continue
		}
		res.IP = ip
		keepHostname(ob, host)
		ob["server"] = ip
		results = append(results, res)
	}
	if err := s.Write(cfg); err != nil {
		return results, err
	}
	return results, nil
}

func pickIPv4(resolve Resolver, host string) (string, error) {
	ips, err := resolve(host)
	if err != nil {
		return "", err
	}
	for _, ip := range ips {
		v4 := ip.To4()
		if v4 == nil {
			continue
		}
		if fakeIPNet.Contains(v4) {
			return "", fmt.Errorf("resolved to fake-ip %s (stale tunnel DNS)", v4)
		}
		return v4.String(), nil
	}
	return "", fmt.Errorf("no IPv4 address")
}

// keepHostname makes sure the hostname survives in SNI and in the HTTP Host
// header of HTTP-based transports before "server" becomes an IP.
func keepHostname(ob map[string]interface{}, host string) {
	if tls, ok := ob["tls"].(map[string]interface{}); ok {
		if enabled, _ := tls["enabled"].(bool); enabled {
			if sni, _ := tls["server_name"].(string); sni == "" {
				tls["server_name"] = host
			}
		}
	}
	tr, ok := ob["transport"].(map[string]interface{})
	if !ok {
		return
	}
	switch t, _ := tr["type"].(string); t {
	case "xhttp", "httpupgrade":
		if h, _ := tr["host"].(string); h == "" {
			tr["host"] = host
		}
	case "http":
		if hs, _ := tr["host"].([]interface{}); len(hs) == 0 {
			tr["host"] = []interface{}{host}
		}
	case "ws":
		headers, _ := tr["headers"].(map[string]interface{})
		if headers == nil {
			headers = map[string]interface{}{}
			tr["headers"] = headers
		}
		if h, _ := headers["Host"].(string); h == "" {
			headers["Host"] = host
		}
	}
}
