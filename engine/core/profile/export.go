// Inverse of parse.go: serialise a stored profile back into a shareable
// import link. Used by the "поделиться" sheet (QR + copy). Kept as a strict
// mirror of ParseURI so a link exported here re-imports to the same config.
package profile

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// ExportURI serialises a stored profile back into a shareable link — the
// inverse of ParseURI. Outbound protocols (hysteria2/vless/trojan/
// shadowsocks) become their scheme:// URI; WireGuard/AmneziaWG endpoints
// become the native [Interface]/[Peer] .conf text (the same shape ParseURI
// accepts). Returns an error for profiles it can't represent as a link.
func ExportURI(p Profile) (string, error) {
	if p.Config == nil {
		return "", fmt.Errorf("профиль без конфигурации")
	}
	typ, _ := p.Config["type"].(string)
	switch strings.ToLower(typ) {
	case "hysteria2":
		return exportHysteria2(p)
	case "vless":
		return exportVLESS(p)
	case "trojan":
		return exportTrojan(p)
	case "shadowsocks":
		return exportShadowsocks(p)
	case "wireguard", "awg":
		return exportWireGuard(p)
	}
	return "", fmt.Errorf("экспорт ссылки не поддерживается для типа %q", typ)
}

func exportHysteria2(p Profile) (string, error) {
	c := p.Config
	host := cfgString(c, "server")
	port := cfgInt(c, "server_port")
	password := cfgString(c, "password")
	if host == "" || port == 0 || password == "" {
		return "", fmt.Errorf("hysteria2: неполный профиль")
	}
	q := url.Values{}
	if tls := cfgMap(c, "tls"); tls != nil {
		if sni := cfgString(tls, "server_name"); sni != "" && sni != host {
			q.Set("sni", sni)
		}
		if b, _ := tls["insecure"].(bool); b {
			q.Set("insecure", "1")
		}
	}
	if obfs := cfgMap(c, "obfs"); obfs != nil {
		if ot := cfgString(obfs, "type"); ot != "" {
			q.Set("obfs", ot)
			if pw := cfgString(obfs, "password"); pw != "" {
				q.Set("obfs-password", pw)
			}
		}
	}
	return buildURI("hysteria2", password, "", host, port, q, p.Label), nil
}

func exportVLESS(p Profile) (string, error) {
	c := p.Config
	host := cfgString(c, "server")
	port := cfgInt(c, "server_port")
	uuid := cfgString(c, "uuid")
	if host == "" || port == 0 || uuid == "" {
		return "", fmt.Errorf("vless: неполный профиль")
	}
	q := url.Values{}
	if flow := cfgString(c, "flow"); flow != "" {
		q.Set("flow", flow)
	}
	writeTLSQuery(q, cfgMap(c, "tls"), host)
	writeTransportQuery(q, cfgMap(c, "transport"))
	return buildURI("vless", uuid, "", host, port, q, p.Label), nil
}

func exportTrojan(p Profile) (string, error) {
	c := p.Config
	host := cfgString(c, "server")
	port := cfgInt(c, "server_port")
	password := cfgString(c, "password")
	if host == "" || port == 0 || password == "" {
		return "", fmt.Errorf("trojan: неполный профиль")
	}
	q := url.Values{}
	writeTLSQuery(q, cfgMap(c, "tls"), host)
	writeTransportQuery(q, cfgMap(c, "transport"))
	return buildURI("trojan", password, "", host, port, q, p.Label), nil
}

func exportShadowsocks(p Profile) (string, error) {
	c := p.Config
	host := cfgString(c, "server")
	port := cfgInt(c, "server_port")
	method := cfgString(c, "method")
	password := cfgString(c, "password")
	if host == "" || port == 0 || method == "" || password == "" {
		return "", fmt.Errorf("shadowsocks: неполный профиль")
	}
	// SIP002: userinfo is base64url(method:password), no padding.
	blob := base64.RawURLEncoding.EncodeToString([]byte(method + ":" + password))
	u := url.URL{
		Scheme:   "ss",
		Host:     net.JoinHostPort(host, strconv.Itoa(port)),
		Fragment: p.Label,
	}
	u.User = url.User(blob)
	return u.String(), nil
}

// exportWireGuard rebuilds the native AmneziaWG/WireGuard .conf text that
// parseAmneziaWGIni accepts. AWG obfuscation params are re-emitted so the
// exported config re-imports byte-for-byte equivalent.
func exportWireGuard(p Profile) (string, error) {
	c := p.Config
	priv := cfgString(c, "private_key")
	peers, _ := c["peers"].([]interface{})
	if priv == "" || len(peers) == 0 {
		return "", fmt.Errorf("wireguard: неполный профиль")
	}
	peer, _ := peers[0].(map[string]interface{})
	if peer == nil {
		return "", fmt.Errorf("wireguard: нет peer")
	}
	pub := cfgString(peer, "public_key")
	host := cfgString(peer, "address")
	port := cfgInt(peer, "port")
	if pub == "" || host == "" || port == 0 {
		return "", fmt.Errorf("wireguard: неполный peer")
	}

	var b strings.Builder
	b.WriteString("[Interface]\n")
	b.WriteString("PrivateKey = " + priv + "\n")
	if addrs := joinIfaceList(c["address"]); addrs != "" {
		b.WriteString("Address = " + addrs + "\n")
	}
	if mtu := cfgInt(c, "mtu"); mtu != 0 {
		b.WriteString("MTU = " + strconv.Itoa(mtu) + "\n")
	}
	// AWG obfuscation params, in the canonical Amnezia order.
	for _, k := range []string{"jc", "jmin", "jmax", "s1", "s2", "s3", "s4"} {
		if v := cfgInt(c, k); v != 0 {
			b.WriteString(iniKey(k) + " = " + strconv.Itoa(v) + "\n")
		}
	}
	for _, k := range []string{"h1", "h2", "h3", "h4", "i1", "i2", "i3", "i4", "i5"} {
		if v := cfgString(c, k); v != "" {
			b.WriteString(strings.ToUpper(k) + " = " + v + "\n")
		}
	}

	b.WriteString("\n[Peer]\n")
	b.WriteString("PublicKey = " + pub + "\n")
	if psk := cfgString(peer, "preshared_key"); psk != "" {
		b.WriteString("PresharedKey = " + psk + "\n")
	}
	if allowed := joinIfaceList(peer["allowed_ips"]); allowed != "" {
		b.WriteString("AllowedIPs = " + allowed + "\n")
	} else {
		b.WriteString("AllowedIPs = 0.0.0.0/0\n")
	}
	b.WriteString("Endpoint = " + net.JoinHostPort(host, strconv.Itoa(port)) + "\n")
	if ka := cfgInt(peer, "persistent_keepalive_interval"); ka != 0 {
		b.WriteString("PersistentKeepalive = " + strconv.Itoa(ka) + "\n")
	}
	return b.String(), nil
}

// buildURI assembles scheme://user[:pass]@host:port?query#label.
func buildURI(scheme, user, pass, host string, port int, q url.Values, label string) string {
	u := url.URL{
		Scheme:   scheme,
		Host:     net.JoinHostPort(host, strconv.Itoa(port)),
		Fragment: label,
	}
	if pass != "" {
		u.User = url.UserPassword(user, pass)
	} else {
		u.User = url.User(user)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// writeTLSQuery emits the v2rayN TLS-ish query params from a sing-box tls
// block — the inverse of buildTLS.
func writeTLSQuery(q url.Values, tls map[string]interface{}, host string) {
	if tls == nil {
		return
	}
	enabled, _ := tls["enabled"].(bool)
	reality := cfgMap(tls, "reality")
	if !enabled && reality == nil {
		return
	}
	if reality != nil {
		q.Set("security", "reality")
		if pbk := cfgString(reality, "public_key"); pbk != "" {
			q.Set("pbk", pbk)
		}
		if sid := cfgString(reality, "short_id"); sid != "" {
			q.Set("sid", sid)
		}
	} else {
		q.Set("security", "tls")
	}
	if sni := cfgString(tls, "server_name"); sni != "" && sni != host {
		q.Set("sni", sni)
	}
	if b, _ := tls["insecure"].(bool); b {
		q.Set("insecure", "1")
	}
	if utls := cfgMap(tls, "utls"); utls != nil {
		if fp := cfgString(utls, "fingerprint"); fp != "" {
			q.Set("fp", fp)
		}
	}
	if alpn, ok := tls["alpn"].([]interface{}); ok && len(alpn) > 0 {
		parts := make([]string, 0, len(alpn))
		for _, a := range alpn {
			if s, ok := a.(string); ok && s != "" {
				parts = append(parts, s)
			}
		}
		if len(parts) > 0 {
			q.Set("alpn", strings.Join(parts, ","))
		}
	}
}

// writeTransportQuery emits type=ws/grpc/http transport params — the inverse
// of buildTransport.
func writeTransportQuery(q url.Values, tr map[string]interface{}) {
	if tr == nil {
		return
	}
	switch strings.ToLower(cfgString(tr, "type")) {
	case "ws":
		q.Set("type", "ws")
		if path := cfgString(tr, "path"); path != "" {
			q.Set("path", path)
		}
		if hdrs := cfgMap(tr, "headers"); hdrs != nil {
			if h := cfgString(hdrs, "Host"); h != "" {
				q.Set("host", h)
			}
		}
	case "grpc":
		q.Set("type", "grpc")
		if svc := cfgString(tr, "service_name"); svc != "" {
			q.Set("serviceName", svc)
		}
	case "http":
		q.Set("type", "http")
		if path := cfgString(tr, "path"); path != "" {
			q.Set("path", path)
		}
		if hs, ok := tr["host"].([]interface{}); ok && len(hs) > 0 {
			if h, ok := hs[0].(string); ok && h != "" {
				q.Set("host", h)
			}
		}
	}
}

// iniKey capitalises an AWG config key ("jmin" → "Jmin") for the .conf.
func iniKey(k string) string {
	if k == "" {
		return k
	}
	return strings.ToUpper(k[:1]) + k[1:]
}

func joinIfaceList(v interface{}) string {
	list, ok := v.([]interface{})
	if !ok {
		return ""
	}
	parts := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok && s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, ", ")
}

// cfgString/cfgInt/cfgMap read a value from a config map tolerating the
// float64 numbers json.Unmarshal produces when profiles.json is re-read.
func cfgString(m map[string]interface{}, key string) string {
	s, _ := m[key].(string)
	return s
}

func cfgInt(m map[string]interface{}, key string) int {
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		n, _ := strconv.Atoi(v)
		return n
	}
	return 0
}

func cfgMap(m map[string]interface{}, key string) map[string]interface{} {
	mm, _ := m[key].(map[string]interface{})
	return mm
}
