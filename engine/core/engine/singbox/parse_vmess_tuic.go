package singbox

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"NekkoVPN/core/profile"
)

// parseVMess maps the v2rayN vmess:// share format (base64 of a JSON object) to
// a sing-box vmess outbound.
func parseVMess(uri string) (Draft, error) {
	body := strings.TrimSpace(uri[len("vmess://"):])
	var raw []byte
	var err error
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if raw, err = enc.DecodeString(body); err == nil {
			break
		}
	}
	if err != nil {
		return Draft{}, fmt.Errorf("vmess: ссылка не декодируется")
	}
	var link map[string]interface{}
	if err := json.Unmarshal(raw, &link); err != nil {
		return Draft{}, fmt.Errorf("vmess: неверный формат ссылки")
	}
	text := func(key string) string {
		switch value := link[key].(type) {
		case string:
			return value
		case float64:
			return strconv.FormatFloat(value, 'f', -1, 64)
		}
		return ""
	}
	host, id := text("add"), text("id")
	port, err := strconv.Atoi(text("port"))
	if host == "" || err != nil || port <= 0 {
		return Draft{}, fmt.Errorf("vmess: не указан host:port")
	}
	if id == "" {
		return Draft{}, fmt.Errorf("vmess: нет UUID в ссылке")
	}
	cipher := text("scy")
	if cipher == "" {
		cipher = "auto"
	}
	alterID, _ := strconv.Atoi(text("aid"))
	cfg := map[string]interface{}{
		"type":        "vmess",
		"server":      host,
		"server_port": port,
		"uuid":        id,
		"security":    cipher,
		"alter_id":    alterID,
	}

	q := url.Values{}
	for key, source := range map[string]string{"sni": "sni", "fp": "fp", "alpn": "alpn", "host": "host", "path": "path", "allowInsecure": "allowInsecure"} {
		if value := text(source); value != "" {
			q.Set(key, value)
		}
	}
	if security := text("tls"); security == "tls" {
		q.Set("security", "tls")
		if tls := buildTLS(q, host); tls != nil {
			cfg["tls"] = tls
		}
	}
	network := strings.ToLower(text("net"))
	switch network {
	case "", "tcp", "raw":
	case "grpc":
		q.Set("serviceName", text("path"))
		q.Set("type", network)
	default:
		q.Set("type", network)
	}
	if network != "" && network != "tcp" && network != "raw" {
		tr := buildTransport(q, host)
		if tr == nil {
			return Draft{}, fmt.Errorf("vmess: неподдерживаемый транспорт %q", network)
		}
		cfg["transport"] = tr
	}
	label := text("ps")
	if label == "" {
		label = host
	}
	return Draft{Kind: profile.KindOutbound, Label: label, Config: cfg}, nil
}

// parseTUIC maps a tuic://uuid:password@host:port URI to a sing-box outbound.
// TUIC runs over QUIC, so TLS is not optional.
func parseTUIC(u *url.URL) (Draft, error) {
	if u.User == nil || u.User.Username() == "" {
		return Draft{}, fmt.Errorf("tuic: нет UUID в ссылке")
	}
	host, port, err := splitHostPort(u, "tuic")
	if err != nil {
		return Draft{}, err
	}
	q := u.Query()
	q.Set("security", "tls")
	if isTruthy(q.Get("allow_insecure")) {
		q.Set("insecure", "1")
	}
	password, _ := u.User.Password()
	cfg := map[string]interface{}{
		"type":        "tuic",
		"server":      host,
		"server_port": port,
		"uuid":        u.User.Username(),
		"password":    password,
		"tls":         buildTLS(q, host),
	}
	if congestion := q.Get("congestion_control"); congestion != "" {
		cfg["congestion_control"] = congestion
	}
	if relay := q.Get("udp_relay_mode"); relay != "" {
		cfg["udp_relay_mode"] = relay
	}
	return Draft{Kind: profile.KindOutbound, Label: labelOrHost(u, host), Config: cfg}, nil
}
