package subscription

import (
	"NekkoVPN/core/naive"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Providers pick the answer format from the User-Agent, so a subscription can
// be a whole client config rather than a list of links. Every format is reduced
// to links here; the decoders stay the only place that understands servers.

// expandConfigDocument returns the links held by a client config. ok is false
// when the input is not one. A server this client cannot dial is left out,
// which gives the same result on every sync.
func expandConfigDocument(input string) (links string, ok bool) {
	input = strings.TrimSpace(input)
	if links, ok := expandSingBoxConfig(input); ok {
		return links, true
	}
	if links, ok := expandXrayConfigs(input); ok {
		return links, true
	}
	return expandClashConfig(input)
}

func expandSingBoxConfig(input string) (string, bool) {
	if !strings.HasPrefix(input, "{") {
		return "", false
	}
	var config struct {
		Outbounds []map[string]interface{} `json:"outbounds"`
	}
	if json.Unmarshal([]byte(input), &config) != nil || len(config.Outbounds) == 0 {
		return "", false
	}
	var out []string
	for _, outbound := range config.Outbounds {
		if field(outbound, "type") == "" {
			return "", false
		}
		if link, err := singBoxOutboundLink(outbound); err == nil {
			out = append(out, link)
		}
	}
	return strings.Join(out, "\n"), true
}

func singBoxOutboundLink(o map[string]interface{}) (string, error) {
	if t := field(o, "type"); t == "naive" || t == "hikkari" {
		l, err := naive.FromOutbound(o, field(o, "tag"))
		if err != nil {
			return "", err
		}
		return l.URI(), nil
	}
	q := url.Values{}
	tls := nested(o, "tls")
	if flag(tls, "enabled") {
		q.Set("security", "tls")
		setNonEmpty(q, "sni", field(tls, "server_name"))
		setNonEmpty(q, "alpn", list(tls, "alpn"))
		setNonEmpty(q, "fp", field(nested(tls, "utls"), "fingerprint"))
		if flag(tls, "insecure") {
			q.Set("allowInsecure", "1")
		}
		if reality := nested(tls, "reality"); flag(reality, "enabled") {
			q.Set("security", "reality")
			setNonEmpty(q, "pbk", field(reality, "public_key"))
			setNonEmpty(q, "sid", field(reality, "short_id"))
		}
		if list(tls, "certificate_public_key_sha256") != "" {
			return "", fmt.Errorf("пиннинг сертификата не поддерживается")
		}
	}
	transport := nested(o, "transport")
	switch network := field(transport, "type"); network {
	case "":
	case "ws":
		q.Set("type", network)
		setNonEmpty(q, "path", field(transport, "path"))
		setNonEmpty(q, "host", field(nested(transport, "headers"), "Host"))
	case "grpc":
		q.Set("type", network)
		setNonEmpty(q, "serviceName", field(transport, "service_name"))
	case "http", "httpupgrade", "xhttp":
		q.Set("type", network)
		setNonEmpty(q, "path", field(transport, "path"))
		host := field(transport, "host")
		if host == "" {
			host = list(transport, "host")
		}
		setNonEmpty(q, "host", host)
		setNonEmpty(q, "mode", field(transport, "mode"))
	default:
		return "", fmt.Errorf("неподдерживаемый транспорт: %s", network)
	}
	return serverLink(server{
		protocol:   field(o, "type"),
		host:       field(o, "server"),
		port:       field(o, "server_port"),
		label:      field(o, "tag"),
		uuid:       field(o, "uuid"),
		password:   field(o, "password"),
		flow:       field(o, "flow"),
		cipher:     firstOf(field(o, "method"), field(o, "security")),
		alterID:    field(o, "alter_id"),
		obfs:       field(nested(o, "obfs"), "type"),
		obfsSecret: field(nested(o, "obfs"), "password"),
		congestion: field(o, "congestion_control"),
		udpRelay:   field(o, "udp_relay_mode"),
	}, q)
}

func expandClashConfig(input string) (string, bool) {
	if !strings.HasPrefix(input, "proxies:") && !strings.Contains(input, "\nproxies:") {
		return "", false
	}
	var config struct {
		Proxies []map[string]interface{} `yaml:"proxies"`
	}
	if yaml.Unmarshal([]byte(input), &config) != nil {
		return "", false
	}
	var out []string
	for _, proxy := range config.Proxies {
		if link, err := clashProxyLink(proxy); err == nil {
			out = append(out, link)
		}
	}
	return strings.Join(out, "\n"), true
}

func clashProxyLink(p map[string]interface{}) (string, error) {
	if field(p, "fingerprint") != "" {
		return "", fmt.Errorf("пиннинг сертификата не поддерживается")
	}
	if field(p, "plugin") != "" {
		return "", fmt.Errorf("плагины shadowsocks не поддерживаются")
	}
	q := url.Values{}
	reality := nested(p, "reality-opts")
	switch {
	case field(reality, "public-key") != "":
		q.Set("security", "reality")
		q.Set("pbk", field(reality, "public-key"))
		setNonEmpty(q, "sid", field(reality, "short-id"))
	case flag(p, "tls"):
		q.Set("security", "tls")
	}
	setNonEmpty(q, "sni", firstOf(field(p, "servername"), field(p, "sni")))
	setNonEmpty(q, "fp", field(p, "client-fingerprint"))
	setNonEmpty(q, "alpn", list(p, "alpn"))
	if flag(p, "skip-cert-verify") {
		q.Set("allowInsecure", "1")
	}
	switch network := field(p, "network"); network {
	case "", "tcp":
	case "ws":
		ws := nested(p, "ws-opts")
		q.Set("type", "ws")
		if flag(ws, "v2ray-http-upgrade") {
			q.Set("type", "httpupgrade")
		}
		setNonEmpty(q, "path", field(ws, "path"))
		setNonEmpty(q, "host", field(nested(ws, "headers"), "Host"))
	case "grpc":
		q.Set("type", "grpc")
		setNonEmpty(q, "serviceName", field(nested(p, "grpc-opts"), "grpc-service-name"))
	case "h2":
		h2 := nested(p, "h2-opts")
		q.Set("type", "http")
		setNonEmpty(q, "path", field(h2, "path"))
		setNonEmpty(q, "host", list(h2, "host"))
	case "xhttp":
		xhttp := nested(p, "xhttp-opts")
		q.Set("type", "xhttp")
		setNonEmpty(q, "path", field(xhttp, "path"))
		setNonEmpty(q, "host", field(xhttp, "host"))
		setNonEmpty(q, "mode", field(xhttp, "mode"))
	default:
		return "", fmt.Errorf("неподдерживаемый транспорт: %s", network)
	}
	return serverLink(server{
		protocol:   field(p, "type"),
		host:       field(p, "server"),
		port:       field(p, "port"),
		label:      field(p, "name"),
		uuid:       field(p, "uuid"),
		password:   field(p, "password"),
		flow:       field(p, "flow"),
		cipher:     field(p, "cipher"),
		alterID:    field(p, "alterId"),
		obfs:       field(p, "obfs"),
		obfsSecret: field(p, "obfs-password"),
		congestion: field(p, "congestion-controller"),
		udpRelay:   field(p, "udp-relay-mode"),
	}, q)
}

// server is what a config says about one server, apart from the TLS and
// transport query the link shares across protocols.
type server struct {
	protocol, host, port, label  string
	uuid, password, flow, cipher string
	alterID, obfs, obfsSecret    string
	congestion, udpRelay         string
}

func serverLink(s server, q url.Values) (string, error) {
	if s.host == "" || s.port == "" || s.port == "0" {
		return "", fmt.Errorf("%s: не указан host:port", s.protocol)
	}
	u := url.URL{Host: net.JoinHostPort(s.host, s.port), Fragment: s.label}
	switch strings.ToLower(s.protocol) {
	case "vless":
		if s.uuid == "" {
			return "", fmt.Errorf("vless: нет UUID")
		}
		u.Scheme, u.User = "vless", url.User(s.uuid)
		setNonEmpty(q, "flow", s.flow)
	case "trojan":
		if s.password == "" {
			return "", fmt.Errorf("trojan: нет пароля")
		}
		u.Scheme, u.User = "trojan", url.User(s.password)
	case "ss", "shadowsocks":
		if s.cipher == "" || s.password == "" {
			return "", fmt.Errorf("shadowsocks: нет метода или пароля")
		}
		creds := base64.RawURLEncoding.EncodeToString([]byte(s.cipher + ":" + s.password))
		u.Scheme, u.User, q = "ss", url.User(creds), url.Values{}
	case "hysteria2", "hy2":
		if s.password == "" {
			return "", fmt.Errorf("hysteria2: нет пароля")
		}
		u.Scheme, u.User = "hysteria2", url.User(s.password)
		setNonEmpty(q, "obfs", s.obfs)
		setNonEmpty(q, "obfs-password", s.obfsSecret)
	case "tuic":
		if s.uuid == "" {
			return "", fmt.Errorf("tuic: нет UUID")
		}
		u.Scheme, u.User = "tuic", url.UserPassword(s.uuid, s.password)
		setNonEmpty(q, "congestion_control", s.congestion)
		setNonEmpty(q, "udp_relay_mode", s.udpRelay)
	case "vmess":
		if s.uuid == "" {
			return "", fmt.Errorf("vmess: нет UUID")
		}
		return vmessLink(s, q)
	default:
		return "", fmt.Errorf("неподдерживаемый протокол: %s", s.protocol)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// vmessLink writes the v2rayN share format: base64 of a JSON object.
func vmessLink(s server, q url.Values) (string, error) {
	port, err := strconv.Atoi(s.port)
	if err != nil {
		return "", fmt.Errorf("vmess: неверный порт %q", s.port)
	}
	alterID, _ := strconv.Atoi(s.alterID)
	network := firstOf(q.Get("type"), "tcp")
	path := q.Get("path")
	if network == "grpc" {
		path = q.Get("serviceName")
	}
	raw, err := json.Marshal(map[string]interface{}{
		"v": "2", "ps": s.label, "add": s.host, "port": port, "id": s.uuid, "aid": alterID,
		"scy": firstOf(s.cipher, "auto"), "net": network, "host": q.Get("host"), "path": path,
		"tls": q.Get("security"), "sni": q.Get("sni"), "alpn": q.Get("alpn"), "fp": q.Get("fp"),
		"allowInsecure": q.Get("allowInsecure"),
	})
	if err != nil {
		return "", err
	}
	return "vmess://" + base64.StdEncoding.EncodeToString(raw), nil
}

func nested(m map[string]interface{}, key string) map[string]interface{} {
	value, _ := m[key].(map[string]interface{})
	return value
}

// field reads a scalar whichever way the format spells it: 443 and "443" alike.
func field(m map[string]interface{}, key string) string {
	switch value := m[key].(type) {
	case string:
		return value
	case int:
		return strconv.Itoa(value)
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	}
	return ""
}

func flag(m map[string]interface{}, key string) bool {
	value, _ := m[key].(bool)
	return value
}

func list(m map[string]interface{}, key string) string {
	values, _ := m[key].([]interface{})
	parts := make([]string, 0, len(values))
	for _, value := range values {
		if text, ok := value.(string); ok {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, ",")
}

func firstOf(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
