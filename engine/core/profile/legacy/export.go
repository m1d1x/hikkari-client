// Package legacy migrates stored profiles without loading any runtime engine.
// Inverse of parse.go: serialise a stored profile back into a shareable
// import link. Used by the "поделиться" sheet (QR + copy). Kept as a strict
// mirror of ParseURI so a link exported here re-imports to the same config.
package legacy

import (
	"NekkoVPN/core/naive"
	"NekkoVPN/core/profile"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
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
func ExportURI(p profile.Profile) (string, error) {
	if p.Config == nil {
		return "", fmt.Errorf("профиль без конфигурации")
	}
	typ, _ := p.Config["type"].(string)
	switch strings.ToLower(typ) {
	case "naive":
		l, err := naive.FromOutbound(p.Config, p.Label)
		if err != nil {
			return "", err
		}
		return l.URI(), nil
	case "hysteria2":
		return exportHysteria2(p)
	case "vless":
		return exportVLESS(p)
	case "trojan":
		return exportTrojan(p)
	case "shadowsocks":
		return exportShadowsocks(p)
	case "vmess":
		return exportVMess(p)
	case "tuic":
		return exportTUIC(p)
	case "wireguard", "awg":
		return exportWireGuard(p)
	}
	return "", fmt.Errorf("экспорт ссылки не поддерживается для типа %q", typ)
}

func exportHysteria2(p profile.Profile) (string, error) {
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
		pin, err := hysteriaPin(tls)
		if err != nil {
			return "", err
		}
		if pin != "" {
			q.Set("hikkari-spki", pin)
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

// hysteriaPin is the link form of tls.certificate_public_key_sha256: the hub's
// leaf is self-signed, so a link that lost the pin cannot connect at all. The
// slice is []string fresh from ParseURI and []interface{} back from JSON. A pin
// the link cannot carry is refused, not dropped.
func hysteriaPin(tls map[string]interface{}) (string, error) {
	var pins []string
	switch v := tls["certificate_public_key_sha256"].(type) {
	case []string:
		pins = v
	case []interface{}:
		for _, e := range v {
			if s, ok := e.(string); ok {
				pins = append(pins, s)
			}
		}
	}
	if len(pins) == 0 {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(pins[0])
	if err != nil || len(pins) > 1 {
		return "", fmt.Errorf("hysteria2: pin сертификата не помещается в ссылку")
	}
	return hex.EncodeToString(raw), nil
}

func exportVLESS(p profile.Profile) (string, error) {
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

func exportTrojan(p profile.Profile) (string, error) {
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

func exportShadowsocks(p profile.Profile) (string, error) {
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
func exportWireGuard(p profile.Profile) (string, error) {
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
	if dns := joinIfaceList(c["dns"]); dns != "" {
		b.WriteString("DNS = " + dns + "\n")
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
	// The first-packet generator's request (Ip, Id, Ib) stands in for I1: the
	// parser dropped I1 for it, so losing these here would send the handshake
	// with no masquerade at all.
	for _, k := range []string{"ip", "id", "ib"} {
		if v := cfgString(c, k); v != "" {
			b.WriteString(strings.ToUpper(k[:1]) + k[1:] + " = " + v + "\n")
		}
	}
	// AmneziaWG 3.x: a server with header protection or the 3.x timers does not
	// answer a client that lacks them, so each one the profile holds goes out.
	for _, f := range AWG3Fields {
		if v := f.exportAWG3(c); v != "" {
			b.WriteString(f.Ini + " = " + v + "\n")
		}
	}

	b.WriteString("\n[Peer]\n")
	b.WriteString("PublicKey = " + pub + "\n")
	// "preshared_key" is how 1.0.34 and earlier stored it; the core reads only
	// "pre_shared_key".
	psk := cfgString(peer, "pre_shared_key")
	if psk == "" {
		psk = cfgString(peer, "preshared_key")
	}
	if psk != "" {
		b.WriteString("PresharedKey = " + psk + "\n")
	}
	if allowed := joinIfaceList(peer["allowed_ips"]); allowed != "" {
		b.WriteString("AllowedIPs = " + allowed + "\n")
	} else {
		b.WriteString("AllowedIPs = 0.0.0.0/0\n")
	}
	b.WriteString("Endpoint = " + net.JoinHostPort(host, strconv.Itoa(port)) + "\n")
	// A number, or under AmneziaWG 3.x a "min-max" range.
	if ka := rangeText(peer["persistent_keepalive_interval"]); ka != "" && ka != "0" {
		b.WriteString("PersistentKeepalive = " + ka + "\n")
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
	case "httpupgrade":
		q.Set("type", "httpupgrade")
		if path := cfgString(tr, "path"); path != "" {
			q.Set("path", path)
		}
		if h := cfgString(tr, "host"); h != "" {
			q.Set("host", h)
		}
	case "xhttp", "splithttp":
		q.Set("type", "xhttp")
		if path := cfgString(tr, "path"); path != "" {
			q.Set("path", path)
		}
		if h := cfgString(tr, "host"); h != "" {
			q.Set("host", h)
		}
		if m := cfgString(tr, "mode"); m != "" {
			q.Set("mode", m)
		}
		if extra := xhttpExtra(tr); extra != "" {
			q.Set("extra", extra)
		}
	}
}

// XHTTPExtraFields maps the camelCase keys of Xray's xhttpSettings (the &extra=
// blob of a link) onto the sing-box-lx transport options, for both directions:
// ParseURI reads them, ExportURI writes them back, and an import goes through
// both, so a field missing on either side is lost. Fields only a server reads
// are left out: on a client they would suggest a setting that has no effect.
var XHTTPExtraFields = map[string]string{
	"xPaddingBytes":        "x_padding_bytes",
	"xPaddingObfsMode":     "x_padding_obfs_mode",
	"xPaddingKey":          "x_padding_key",
	"xPaddingHeader":       "x_padding_header",
	"xPaddingPlacement":    "x_padding_placement",
	"xPaddingMethod":       "x_padding_method",
	"noGRPCHeader":         "no_grpc_header",
	"sessionPlacement":     "session_placement",
	"sessionKey":           "session_key",
	"seqPlacement":         "seq_placement",
	"seqKey":               "seq_key",
	"uplinkDataPlacement":  "uplink_data_placement",
	"uplinkDataKey":        "uplink_data_key",
	"uplinkChunkSize":      "uplink_chunk_size",
	"uplinkHTTPMethod":     "uplink_http_method",
	"scMaxEachPostBytes":   "sc_max_each_post_bytes",
	"scMinPostsIntervalMs": "sc_min_posts_interval_ms",
}

// XHTTPExtraXmuxFields is the same mapping for the nested xmux object.
var XHTTPExtraXmuxFields = map[string]string{
	"maxConcurrency":   "max_concurrency",
	"maxConnections":   "max_connections",
	"cMaxReuseTimes":   "c_max_reuse_times",
	"hMaxRequestTimes": "h_max_request_times",
	"hMaxReusableSecs": "h_max_reusable_secs",
	"hKeepAlivePeriod": "h_keep_alive_period",
}

// xhttpExtra is the inverse of the parser's applyXHTTPExtra. Empty when the
// transport carries no tuning, so plain links stay as short as they came.
func xhttpExtra(tr map[string]interface{}) string {
	extra := map[string]interface{}{}
	for from, to := range XHTTPExtraFields {
		if value, ok := tr[to]; ok {
			extra[from] = value
		}
	}
	if headers, ok := tr["headers"].(map[string]interface{}); ok && len(headers) > 0 {
		extra["headers"] = headers
	}
	if options := cfgMap(tr, "xmux"); len(options) > 0 {
		xmux := map[string]interface{}{}
		for from, to := range XHTTPExtraXmuxFields {
			if value, ok := options[to]; ok {
				xmux[from] = value
			}
		}
		if len(xmux) > 0 {
			extra["xmux"] = xmux
		}
	}
	if len(extra) == 0 {
		return ""
	}
	raw, err := json.Marshal(extra)
	if err != nil {
		return ""
	}
	return string(raw)
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
