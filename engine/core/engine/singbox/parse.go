// URI parsers for "import-by-link" profiles. ParseURI dispatches on the URI
// scheme and returns a profile draft (kind + label + sing-box config block)
// ready to hand to Store.Add. Add new schemes by appending a case to the
// switch and a parser function below.
package singbox

import (
	"bytes"
	"compress/zlib"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"

	"NekkoVPN/core/profile"
	"NekkoVPN/core/profile/legacy"
	"NekkoVPN/core/subscription"
)

// Draft is the parsed result of an import URI. Kind/Config are what
// Store.Add wants; Label is the human-readable name taken from the URI
// fragment (or falls back to host).
type Draft struct {
	Kind   string
	Label  string
	Config map[string]interface{}
}

// ErrUnsupportedClient is shared with the application's provider service.
var ErrUnsupportedClient = subscription.ErrUnsupportedClient

// ParseURI decodes one protocol link or INI; provider URLs belong to subscription.
func ParseURI(uri string) (Draft, error) {
	uri = subscription.NormalizeLink(uri)
	if uri == "" {
		return Draft{}, fmt.Errorf("пустая ссылка")
	}
	if looksLikeWireGuardIni(uri) {
		return parseAmneziaWGIni(uri)
	}
	if strings.HasPrefix(strings.ToLower(uri), "vpn://") {
		return parseAmneziaVPN(uri)
	}
	u, err := url.Parse(uri)
	if err != nil {
		return Draft{}, fmt.Errorf("не удалось разобрать ссылку: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "hysteria2", "hy2":
		return parseHysteria2(u)
	case "vless":
		return parseVLESS(u)
	case "trojan":
		return parseTrojan(u)
	case "ss":
		return parseShadowsocks(uri, u)
	case "vpn":
		return parseAmneziaVPN(uri)
	}
	return Draft{}, fmt.Errorf("неподдерживаемая схема: %s", u.Scheme)
}

// ParseURIs is retained for protocol/parser compatibility tests.
func ParseURIs(input string) ([]Draft, error) { return ParseURIsForDevice(input, "") }
func ParseURIsForDevice(input, deviceID string) ([]Draft, error) {
	return subscription.Parse(input, deviceID, ParseURI)
}

// parseHysteria2 maps a hysteria2:// URI to the sing-box outbound shape.
// Reference: https://v2.hysteria.network/docs/developers/URI-Scheme/
func parseHysteria2(u *url.URL) (Draft, error) {
	// Refuse unsupported certificate pinning/Noise instead of dropping them.
	if u.Query().Get("pinSHA256") != "" || u.Query().Get("finalmask") != "" {
		return Draft{}, fmt.Errorf("hysteria2: pinSHA256 и полный набор масок требуют движок Xray")
	}

	if u.User == nil || u.User.Username() == "" {
		return Draft{}, fmt.Errorf("hysteria2: в ссылке нет пароля")
	}
	password := u.User.Username()
	host := u.Hostname()
	portStr := u.Port()
	if host == "" || portStr == "" {
		return Draft{}, fmt.Errorf("hysteria2: не указан host:port")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return Draft{}, fmt.Errorf("hysteria2: неверный порт %q", portStr)
	}

	q := u.Query()
	sni := q.Get("sni")
	if sni == "" {
		sni = host
	}
	tls := map[string]interface{}{
		"enabled":     true,
		"server_name": sni,
	}
	if isTruthy(q.Get("insecure")) {
		tls["insecure"] = true
	}
	// hikkari-spki: the hub's pin for a self-signed leaf, hex SHA-256 of its
	// SubjectPublicKeyInfo — the one pin sing-box takes. A pin that does not
	// decode is refused, not dropped: the link would dial unverified.
	if spki := q.Get("hikkari-spki"); spki != "" {
		pin, err := hex.DecodeString(spki)
		if err != nil || len(pin) != sha256.Size {
			return Draft{}, fmt.Errorf("hysteria2: неверный hikkari-spki")
		}
		tls["certificate_public_key_sha256"] = []string{base64.StdEncoding.EncodeToString(pin)}
	}

	cfg := map[string]interface{}{
		"type":        "hysteria2",
		"server":      host,
		"server_port": port,
		"password":    password,
		"tls":         tls,
	}

	if obfs := q.Get("obfs"); obfs != "" {
		ob := map[string]interface{}{"type": obfs}
		if pw := q.Get("obfs-password"); pw != "" {
			ob["password"] = pw
		}
		cfg["obfs"] = ob
	}

	label := u.Fragment
	if label != "" {
		if decoded, derr := url.QueryUnescape(label); derr == nil {
			label = decoded
		}
	}
	if label == "" {
		label = host
	}

	return Draft{Kind: profile.KindOutbound, Label: label, Config: cfg}, nil
}

func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// parseVLESS maps a VLESS URI (v2rayN format) to a sing-box vless outbound.
// Reference: https://github.com/XTLS/Xray-core/discussions/716
func parseVLESS(u *url.URL) (Draft, error) {
	if u.User == nil || u.User.Username() == "" {
		return Draft{}, fmt.Errorf("vless: нет UUID в ссылке")
	}
	host, port, err := splitHostPort(u, "vless")
	if err != nil {
		return Draft{}, err
	}
	q := u.Query()
	cfg := map[string]interface{}{
		"type":        "vless",
		"server":      host,
		"server_port": port,
		"uuid":        u.User.Username(),
	}
	pe := q.Get("packetEncoding")
	if pe == "" {
		pe = "xudp"
	}
	cfg["packet_encoding"] = pe
	if flow := q.Get("flow"); flow != "" {
		cfg["flow"] = flow
	}
	if tls := buildTLS(q, host); tls != nil {
		cfg["tls"] = tls
	}
	if tr := buildTransport(q, host); tr != nil {
		cfg["transport"] = tr
	}
	return Draft{Kind: profile.KindOutbound, Label: labelOrHost(u, host), Config: cfg}, nil
}

// parseTrojan maps a trojan:// URI to a sing-box trojan outbound.
func parseTrojan(u *url.URL) (Draft, error) {
	if u.User == nil || u.User.Username() == "" {
		return Draft{}, fmt.Errorf("trojan: нет пароля в ссылке")
	}
	host, port, err := splitHostPort(u, "trojan")
	if err != nil {
		return Draft{}, err
	}
	q := u.Query()
	cfg := map[string]interface{}{
		"type":        "trojan",
		"server":      host,
		"server_port": port,
		"password":    u.User.Username(),
	}
	// Trojan implies TLS by default — build it whether security= is set or not.
	if tls := buildTLS(q, host); tls != nil {
		cfg["tls"] = tls
	} else {
		cfg["tls"] = map[string]interface{}{"enabled": true, "server_name": host}
	}
	if tr := buildTransport(q, host); tr != nil {
		cfg["transport"] = tr
	}
	return Draft{Kind: profile.KindOutbound, Label: labelOrHost(u, host), Config: cfg}, nil
}

// parseShadowsocks maps an ss:// URI to a sing-box shadowsocks outbound.
// Handles both forms:
//
//	ss://method:password@host:port#label   (plain)
//	ss://BASE64(method:password)@host:port#label  (legacy)
//	ss://BASE64(method:password@host:port)#label  (older, whole-URI base64)
//
// The fully-base64 legacy form is detected when u.Host is empty after
// net/url's parse — we then re-decode the userinfo portion of the original
// raw URI ourselves.
func parseShadowsocks(rawURI string, u *url.URL) (Draft, error) {
	host := u.Hostname()
	port := u.Port()
	var method, password string

	if host == "" {
		// Whole-URI base64 form: re-parse after decoding the userinfo blob.
		body := strings.TrimPrefix(rawURI, "ss://")
		if hash := strings.Index(body, "#"); hash >= 0 {
			body = body[:hash]
		}
		decoded, err := decodeSSBlob(body)
		if err != nil {
			return Draft{}, fmt.Errorf("ss: не удалось разобрать ссылку")
		}
		at := strings.LastIndex(decoded, "@")
		if at < 0 {
			return Draft{}, fmt.Errorf("ss: нет user@host в ссылке")
		}
		creds := decoded[:at]
		hostPort := decoded[at+1:]
		colon := strings.LastIndex(hostPort, ":")
		if colon < 0 {
			return Draft{}, fmt.Errorf("ss: не указан порт")
		}
		host = hostPort[:colon]
		port = hostPort[colon+1:]
		method, password = splitSSCreds(creds)
	} else {
		if u.User == nil {
			return Draft{}, fmt.Errorf("ss: нет credentials в ссылке")
		}
		// Userinfo can be either plain "method:password" or base64-encoded.
		raw := u.User.Username()
		if pw, ok := u.User.Password(); ok && pw != "" {
			method, password = raw, pw
		} else {
			decoded, err := decodeSSBlob(raw)
			if err != nil {
				return Draft{}, fmt.Errorf("ss: credentials не декодируются")
			}
			method, password = splitSSCreds(decoded)
		}
	}
	if method == "" || password == "" {
		return Draft{}, fmt.Errorf("ss: пустой метод или пароль")
	}
	if port == "" {
		return Draft{}, fmt.Errorf("ss: не указан порт")
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		return Draft{}, fmt.Errorf("ss: неверный порт %q", port)
	}
	cfg := map[string]interface{}{
		"type":        "shadowsocks",
		"server":      host,
		"server_port": p,
		"method":      method,
		"password":    password,
	}
	return Draft{Kind: profile.KindOutbound, Label: labelOrHost(u, host), Config: cfg}, nil
}

// splitHostPort pulls host + numeric port out of a URL.
func splitHostPort(u *url.URL, scheme string) (string, int, error) {
	host := u.Hostname()
	portStr := u.Port()
	if host == "" || portStr == "" {
		return "", 0, fmt.Errorf("%s: не указан host:port", scheme)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0, fmt.Errorf("%s: неверный порт %q", scheme, portStr)
	}
	return host, port, nil
}

// labelOrHost takes the URI fragment as profile label, decoding URL escapes;
// falls back to the host when the fragment is empty.
func labelOrHost(u *url.URL, host string) string {
	label := u.Fragment
	if label != "" {
		if decoded, derr := url.QueryUnescape(label); derr == nil {
			label = decoded
		}
		return label
	}
	return host
}

// buildTLS reads the common v2rayN/Xray TLS-ish params from the query and
// emits a sing-box tls block. Returns nil when no TLS is requested.
func buildTLS(q url.Values, fallbackSNI string) map[string]interface{} {
	security := strings.ToLower(q.Get("security"))
	if security == "" || security == "none" {
		// VLESS without security= shouldn't have TLS; bail.
		if q.Get("sni") == "" && q.Get("fp") == "" && q.Get("pbk") == "" {
			return nil
		}
	}
	sni := q.Get("sni")
	if sni == "" {
		sni = fallbackSNI
	}
	tls := map[string]interface{}{
		"enabled":     true,
		"server_name": sni,
	}
	if isTruthy(q.Get("insecure")) || isTruthy(q.Get("allowInsecure")) {
		tls["insecure"] = true
	}
	if alpn := q.Get("alpn"); alpn != "" {
		parts := strings.Split(alpn, ",")
		out := make([]interface{}, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, p)
			}
		}
		if len(out) > 0 {
			tls["alpn"] = out
		}
	}
	fp := q.Get("fp")
	if fp == "" && security == "reality" {
		fp = "chrome"
	}
	if fp != "" {
		tls["utls"] = map[string]interface{}{
			"enabled":     true,
			"fingerprint": fp,
		}
	}
	if security == "reality" {
		reality := map[string]interface{}{"enabled": true}
		if pbk := q.Get("pbk"); pbk != "" {
			reality["public_key"] = pbk
		}
		if sid := q.Get("sid"); sid != "" {
			reality["short_id"] = sid
		}
		tls["reality"] = reality
	}
	return tls
}

// buildTransport reads transport-layer params (type=ws/grpc/http/xhttp/httpupgrade) and emits
// the sing-box transport block. Returns nil for plain TCP.
func buildTransport(q url.Values, fallbackHost string) map[string]interface{} {
	t := strings.ToLower(q.Get("type"))
	switch t {
	case "ws", "websocket":
		tr := map[string]interface{}{"type": "ws"}
		if path := q.Get("path"); path != "" {
			tr["path"] = path
		}
		if hostHdr := q.Get("host"); hostHdr != "" {
			tr["headers"] = map[string]interface{}{"Host": hostHdr}
		}
		return tr
	case "grpc":
		tr := map[string]interface{}{"type": "grpc"}
		if svc := q.Get("serviceName"); svc != "" {
			tr["service_name"] = svc
		}
		return tr
	case "http", "h2":
		tr := map[string]interface{}{"type": "http"}
		if path := q.Get("path"); path != "" {
			tr["path"] = path
		}
		if hostHdr := q.Get("host"); hostHdr != "" {
			tr["host"] = []interface{}{hostHdr}
		}
		return tr
	case "httpupgrade":
		tr := map[string]interface{}{"type": "httpupgrade"}
		if path := q.Get("path"); path != "" {
			tr["path"] = path
		}
		hostHdr := q.Get("host")
		if hostHdr == "" {
			hostHdr = fallbackHost
		}
		if hostHdr != "" {
			tr["host"] = hostHdr
		}
		return tr
	case "xhttp", "splithttp":
		tr := map[string]interface{}{"type": "xhttp"}
		if path := q.Get("path"); path != "" {
			tr["path"] = path
		}
		hostHdr := q.Get("host")
		if hostHdr == "" {
			hostHdr = fallbackHost
		}
		if hostHdr != "" {
			tr["host"] = hostHdr
		}
		mode := q.Get("mode")
		if mode == "" {
			mode = "auto"
		}
		tr["mode"] = mode
		applyXHTTPExtra(tr, q.Get("extra"))
		return tr
	}
	return nil
}

// xhttpStringOptions are the fields the core declares as "min-max" strings, while
// Xray links carry them as a number or a {from,to} object.
var xhttpStringOptions = map[string]bool{
	"x_padding_bytes":          true,
	"uplink_chunk_size":        true,
	"sc_max_each_post_bytes":   true,
	"sc_min_posts_interval_ms": true,
}

// applyXHTTPExtra merges the JSON blob a subscription passes in &extra= into the
// transport. A malformed blob is ignored rather than failing the import: the link
// still connects with default tuning, which is what it did before extra was read.
func applyXHTTPExtra(tr map[string]interface{}, raw string) {
	if raw == "" {
		return
	}
	var extra map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &extra); err != nil {
		return
	}
	for from, to := range legacy.XHTTPExtraFields {
		value, ok := extra[from]
		if !ok {
			continue
		}
		if xhttpStringOptions[to] {
			value = xhttpRange(value)
		}
		if value != nil {
			tr[to] = value
		}
	}
	if headers, ok := extra["headers"].(map[string]interface{}); ok && len(headers) > 0 {
		tr["headers"] = headers
	}
	xmux, _ := extra["xmux"].(map[string]interface{})
	options := map[string]interface{}{}
	for from, to := range legacy.XHTTPExtraXmuxFields {
		value, ok := xmux[from]
		if !ok {
			continue
		}
		if to == "h_keep_alive_period" {
			options[to] = value
		} else if ranged := xhttpRange(value); ranged != nil {
			options[to] = ranged
		}
	}
	if len(options) > 0 {
		tr["xmux"] = options
	}
}

// xhttpRange normalizes Xray's number, "min-max" string and {from,to} object
// spellings to the "min-max" string the core takes. Anything else yields nil.
func xhttpRange(value interface{}) interface{} {
	switch v := value.(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', 0, 64)
	case map[string]interface{}:
		from, okFrom := v["from"].(float64)
		to, okTo := v["to"].(float64)
		if okFrom && okTo {
			return fmt.Sprintf("%.0f-%.0f", from, to)
		}
	}
	return nil
}

// decodeSSBlob tries URL-safe base64 first, then standard. Pads when needed.
func decodeSSBlob(s string) (string, error) {
	pad := func(in string) string {
		if m := len(in) % 4; m != 0 {
			return in + strings.Repeat("=", 4-m)
		}
		return in
	}
	if b, err := base64.URLEncoding.DecodeString(pad(s)); err == nil {
		return string(b), nil
	}
	if b, err := base64.StdEncoding.DecodeString(pad(s)); err == nil {
		return string(b), nil
	}
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return string(b), nil
	}
	if b, err := base64.RawStdEncoding.DecodeString(s); err == nil {
		return string(b), nil
	}
	return "", fmt.Errorf("not base64")
}

func splitSSCreds(creds string) (method, password string) {
	colon := strings.Index(creds, ":")
	if colon < 0 {
		return "", ""
	}
	return creds[:colon], creds[colon+1:]
}

// looksLikeWireGuardIni reports whether the pasted text is a native
// WireGuard/AmneziaWG .conf file rather than a scheme:// URI.
func looksLikeWireGuardIni(s string) bool {
	return strings.Contains(s, "[Interface]") && strings.Contains(s, "[Peer]")
}

// parseAmneziaWGIni parses a native AmneziaWG / WireGuard .conf file (the
// "[Interface]"/"[Peer]" ini format exported by the Amnezia desktop/mobile
// app) into a sing-box "awg" endpoint. Falls back to a plain "wireguard"
// endpoint when none of the Jc/Jmin/... obfuscation params are present.
// Reference shape: archive/config.json's "endpoints[0]" block.
func parseAmneziaWGIni(text string) (Draft, error) {
	iface, peer, err := splitIniSections(text)
	if err != nil {
		return Draft{}, err
	}

	privateKey := iface["PrivateKey"]
	if privateKey == "" {
		return Draft{}, fmt.Errorf("amnezia: в конфиге нет PrivateKey")
	}
	publicKey := peer["PublicKey"]
	if publicKey == "" {
		return Draft{}, fmt.Errorf("amnezia: в конфиге нет PublicKey")
	}
	endpoint := peer["Endpoint"]
	host, portStr, err := net.SplitHostPort(endpoint)
	if err != nil {
		return Draft{}, fmt.Errorf("amnezia: не удалось разобрать Endpoint %q", endpoint)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return Draft{}, fmt.Errorf("amnezia: неверный порт %q", portStr)
	}

	address := splitAndTrim(iface["Address"])
	if len(address) == 0 {
		return Draft{}, fmt.Errorf("amnezia: в конфиге нет Address")
	}
	addrList := make([]interface{}, 0, len(address))
	for _, a := range address {
		addrList = append(addrList, a)
	}

	allowedIPs := splitAndTrim(peer["AllowedIPs"])
	if len(allowedIPs) == 0 {
		allowedIPs = []string{"0.0.0.0/0"}
	}
	allowedList := make([]interface{}, 0, len(allowedIPs))
	for _, a := range allowedIPs {
		allowedList = append(allowedList, a)
	}

	peerCfg := map[string]interface{}{
		"address":     host,
		"port":        port,
		"public_key":  publicKey,
		"allowed_ips": allowedList,
	}
	if psk := peer["PresharedKey"]; psk != "" {
		peerCfg["preshared_key"] = psk
	}
	if ka := peer["PersistentKeepalive"]; ka != "" {
		if n, err := strconv.Atoi(ka); err == nil {
			peerCfg["persistent_keepalive_interval"] = n
		}
	}

	cfg := map[string]interface{}{
		"type":        "wireguard",
		"private_key": privateKey,
		"address":     addrList,
		"peers":       []interface{}{peerCfg},
	}
	if mtu := iface["MTU"]; mtu != "" {
		if n, err := strconv.Atoi(mtu); err == nil {
			cfg["mtu"] = n
		}
	}

	// AmneziaWG obfuscation params — presence of Jc marks this as "awg"
	// rather than plain WireGuard.
	if jc := getIniField(iface, "Jc"); jc != "" {
		cfg["type"] = "awg"
		if n, err := strconv.Atoi(jc); err == nil {
			cfg["jc"] = n
		}
		if n, err := strconv.Atoi(getIniField(iface, "Jmin")); err == nil {
			cfg["jmin"] = n
		}
		if n, err := strconv.Atoi(getIniField(iface, "Jmax")); err == nil {
			cfg["jmax"] = n
		}
		for _, key := range []string{"S1", "S2", "S3", "S4"} {
			if v := getIniField(iface, key); v != "" {
				if n, err := strconv.Atoi(v); err == nil {
					cfg[strings.ToLower(key)] = n
				}
			}
		}
		// H1-H4 and I1-I5 are opaque tokens (plain numbers or "n-n" ranges,
		// or magic-header specs for I*) — pass through as strings verbatim.
		for _, key := range []string{"H1", "H2", "H3", "H4", "I1", "I2", "I3", "I4", "I5"} {
			if v := getIniField(iface, key); v != "" {
				cfg[strings.ToLower(key)] = v
			}
		}
	}

	label := host
	return Draft{Kind: profile.KindEndpoint, Label: label, Config: cfg}, nil
}

// splitIniSections walks a WireGuard-style ini file and returns the
// key/value pairs of the first [Interface] and first [Peer] section.
// Extra [Peer] sections (multi-hop configs) are ignored — only the first
// hop is imported.
func splitIniSections(text string) (iface, peer map[string]string, err error) {
	iface = map[string]string{}
	peer = map[string]string{}
	var cur map[string]string
	sawPeer := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimSuffix(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section := strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			switch section {
			case "interface":
				cur = iface
			case "peer":
				if sawPeer {
					cur = nil // ignore subsequent peers
				} else {
					cur = peer
					sawPeer = true
				}
			default:
				cur = nil
			}
			continue
		}
		if cur == nil {
			continue
		}
		eq := strings.Index(line, "=")
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		cur[key] = val
	}
	if len(iface) == 0 {
		return nil, nil, fmt.Errorf("amnezia: секция [Interface] пустая или не найдена")
	}
	if len(peer) == 0 {
		return nil, nil, fmt.Errorf("amnezia: секция [Peer] пустая или не найдена")
	}
	return iface, peer, nil
}

// splitAndTrim splits a comma-separated ini value into trimmed, non-empty
// parts.
func splitAndTrim(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func getIniField(m map[string]string, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key]; ok {
		return v
	}
	for k, v := range m {
		if strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}

const maxAmneziaDecompressedSize = 1024 * 1024 // 1 MiB

// parseAmneziaVPN decodes native Amnezia VPN "vpn://..." configuration links.
// Amnezia format:
// 1. "vpn://" prefix followed by Base64URL string (padded or unpadded).
// 2. Binary payload compressed via Qt qCompress:
//    - Bytes 0..3: big-endian uint32 uncompressed size.
//    - Bytes 4..: standard zlib stream.
//    - Fallbacks: raw zlib from byte 0, or uncompressed JSON.
// 3. Decompression limit: capped at 1 MiB via io.LimitReader against zip-bombs.
// 4. JSON container:
//    - description or hostName used as profile label.
//    - awg / wireguard container: extracts INI from last_config supplemented
//      with Jc/Jmin/Jmax/S1/S2/H1..H4 from container JSON.
//    - xray / vless container: converts to sing-box vless outbound with reality / TLS / transport.
//    - shadowsocks container: converts to sing-box shadowsocks outbound.
func parseAmneziaVPN(rawURI string) (Draft, error) {
	uri := strings.TrimSpace(rawURI)
	var uriFragment string
	if hash := strings.Index(uri, "#"); hash >= 0 {
		uriFragment = uri[hash+1:]
		if unescaped, err := url.QueryUnescape(uriFragment); err == nil {
			uriFragment = unescaped
		}
		uri = uri[:hash]
	}
	if q := strings.Index(uri, "?"); q >= 0 {
		uri = uri[:q]
	}
	lower := strings.ToLower(uri)
	if !strings.HasPrefix(lower, "vpn://") {
		return Draft{}, fmt.Errorf("amnezia: ссылка должна начинаться с vpn://")
	}
	encoded := uri[6:]
	encoded = strings.TrimRight(encoded, "/")
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return Draft{}, fmt.Errorf("amnezia: пустые данные ссылки")
	}

	payload, err := decodeAmneziaBase64(encoded)
	if err != nil {
		return Draft{}, fmt.Errorf("amnezia: ошибка base64: %w", err)
	}

	decomp, err := decompressAmneziaPayload(payload)
	if err != nil {
		return Draft{}, err
	}

	return parseAmneziaJSON(decomp, uriFragment)
}

func decodeAmneziaBase64(s string) ([]byte, error) {
	if strings.Contains(s, "%") {
		if unescaped, err := url.QueryUnescape(s); err == nil {
			s = unescaped
		}
	}
	s = strings.TrimSpace(s)
	// 1. RawURLEncoding (standard Amnezia format)
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	// 2. URLEncoding with padding
	pad := func(in string) string {
		if m := len(in) % 4; m != 0 {
			return in + strings.Repeat("=", 4-m)
		}
		return in
	}
	if b, err := base64.URLEncoding.DecodeString(pad(s)); err == nil {
		return b, nil
	}
	// 3. RawStdEncoding
	if b, err := base64.RawStdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	// 4. StdEncoding with padding
	if b, err := base64.StdEncoding.DecodeString(pad(s)); err == nil {
		return b, nil
	}
	return nil, fmt.Errorf("неверная base64 строка")
}

func decompressAmneziaPayload(data []byte) ([]byte, error) {
	tryZlib := func(input []byte) ([]byte, error) {
		zr, err := zlib.NewReader(bytes.NewReader(input))
		if err != nil {
			return nil, err
		}
		defer zr.Close()

		limited := io.LimitReader(zr, int64(maxAmneziaDecompressedSize)+1)
		out, err := io.ReadAll(limited)
		if err != nil {
			return nil, err
		}
		if len(out) > maxAmneziaDecompressedSize {
			return nil, fmt.Errorf("amnezia: распакованный конфиг превышает лимит 1 МиБ")
		}
		return out, nil
	}

	var qtSizeHuge bool
	var claimedQtSize uint32

	// 1. Try Qt qCompress format: 4-byte big-endian uncompressed size + zlib stream
	if len(data) >= 4 {
		claimedQtSize = binary.BigEndian.Uint32(data[:4])
		if claimedQtSize > maxAmneziaDecompressedSize {
			qtSizeHuge = true
		} else {
			if decomp, err := tryZlib(data[4:]); err == nil {
				return decomp, nil
			} else if strings.Contains(err.Error(), "1 МиБ") {
				return nil, err
			}
		}
	}

	// 2. Try raw zlib from 1st byte (without Qt length header)
	if decomp, err := tryZlib(data); err == nil {
		return decomp, nil
	} else if strings.Contains(err.Error(), "1 МиБ") {
		return nil, err
	}

	// 3. Try plain JSON without compression
	trimmed := bytes.TrimSpace(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")))
	if len(trimmed) <= maxAmneziaDecompressedSize && json.Valid(trimmed) {
		return trimmed, nil
	}

	if qtSizeHuge {
		return nil, fmt.Errorf("amnezia: распакованный размер (%d байт) превышает лимит 1 МиБ", claimedQtSize)
	}

	return nil, fmt.Errorf("amnezia: не удалось распаковать или распознать данные конфига")
}

func parseAmneziaJSON(decomp []byte, fragment string) (Draft, error) {
	var root map[string]interface{}
	if err := json.Unmarshal(decomp, &root); err != nil {
		var list []map[string]interface{}
		if errList := json.Unmarshal(decomp, &list); errList == nil && len(list) > 0 {
			root = list[0]
		} else {
			return Draft{}, fmt.Errorf("amnezia: неверный JSON конфига: %w", err)
		}
	}

	if sl, ok := root["serversList"].([]interface{}); ok && len(sl) > 0 {
		if firstServer, ok := sl[0].(map[string]interface{}); ok {
			root = firstServer
		}
	}

	description := getMapString(root, "description")
	hostName := getMapString(root, "hostName", "hostname", "ip", "host")

	label := description
	if label == "" {
		label = hostName
	}
	if label == "" {
		label = fragment
	}

	defaultContainer := getMapString(root, "defaultContainer", "default_container")
	containersRaw, _ := root["containers"].([]interface{})

	containerItem, cType := selectAmneziaContainer(containersRaw, defaultContainer, root)
	if containerItem == nil {
		return Draft{}, fmt.Errorf("amnezia: не найден поддерживаемый контейнер (поддерживаются awg, wireguard, xray, shadowsocks)")
	}

	var draft Draft
	var err error

	switch cType {
	case "awg", "wireguard":
		draft, err = parseAmneziaContainerAWG(containerItem, hostName)
	case "xray", "vless":
		draft, err = parseAmneziaContainerXray(containerItem, hostName)
	case "shadowsocks", "ss":
		draft, err = parseAmneziaContainerShadowsocks(containerItem, hostName)
	default:
		return Draft{}, fmt.Errorf("amnezia: контейнер %q не поддерживается (поддерживаются awg, wireguard, xray, shadowsocks)", cType)
	}
	if err != nil {
		return Draft{}, err
	}

	if label != "" {
		draft.Label = label
	}
	return draft, nil
}

func selectAmneziaContainer(containers []interface{}, defaultContainer string, root map[string]interface{}) (map[string]interface{}, string) {
	normalizedDefault := normalizeContainerName(defaultContainer)

	if normalizedDefault != "" {
		for _, raw := range containers {
			if m, ok := raw.(map[string]interface{}); ok {
				cType, _ := identifyContainerType(m)
				if cType == normalizedDefault {
					return m, cType
				}
			}
		}
	}

	supportedPriority := []string{"awg", "wireguard", "xray", "shadowsocks"}
	for _, pref := range supportedPriority {
		for _, raw := range containers {
			if m, ok := raw.(map[string]interface{}); ok {
				cType, _ := identifyContainerType(m)
				if cType == pref {
					return m, cType
				}
			}
		}
	}

	for _, raw := range containers {
		if m, ok := raw.(map[string]interface{}); ok {
			cType, _ := identifyContainerType(m)
			if cType != "" {
				return m, cType
			}
		}
	}

	if rootType, _ := identifyContainerType(root); rootType != "" {
		return root, rootType
	}

	return nil, ""
}

func identifyContainerType(item map[string]interface{}) (string, map[string]interface{}) {
	if item == nil {
		return "", nil
	}
	name := normalizeContainerName(getMapString(item, "container"))
	if name != "" {
		if sub, ok := item[name].(map[string]interface{}); ok {
			return name, sub
		}
		return name, item
	}
	for _, t := range []string{"awg", "wireguard", "xray", "shadowsocks"} {
		if sub, ok := item[t].(map[string]interface{}); ok {
			return t, sub
		}
	}
	if lc := getMapString(item, "last_config", "lastConfig"); lc != "" {
		if looksLikeWireGuardIni(lc) || strings.Contains(lc, "[Interface]") {
			return "awg", item
		}
		if strings.HasPrefix(lc, "vless://") {
			return "xray", item
		}
		if strings.HasPrefix(lc, "ss://") {
			return "shadowsocks", item
		}
	}
	return "", item
}

func normalizeContainerName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.TrimPrefix(name, "amnezia-")
	if name == "vless" {
		return "xray"
	}
	if name == "ss" {
		return "shadowsocks"
	}
	return name
}

func parseAmneziaContainerAWG(item map[string]interface{}, rootHost string) (Draft, error) {
	var sub map[string]interface{}
	for _, k := range []string{"awg", "wireguard"} {
		if s, ok := item[k].(map[string]interface{}); ok {
			sub = s
			break
		}
	}

	lastConfigRaw := item["last_config"]
	if lastConfigRaw == nil && sub != nil {
		lastConfigRaw = sub["last_config"]
	}
	if lastConfigRaw == nil {
		lastConfigRaw = item["lastConfig"]
		if lastConfigRaw == nil && sub != nil {
			lastConfigRaw = sub["lastConfig"]
		}
	}

	var lastConfigMap map[string]interface{}
	var iniText string

	switch lc := lastConfigRaw.(type) {
	case string:
		lc = strings.TrimSpace(lc)
		if strings.HasPrefix(lc, "{") {
			if err := json.Unmarshal([]byte(lc), &lastConfigMap); err == nil {
				if cfgStr := getMapString(lastConfigMap, "config"); cfgStr != "" {
					iniText = cfgStr
				}
			}
		}
		if iniText == "" && strings.Contains(lc, "[Interface]") {
			iniText = lc
		}
	case map[string]interface{}:
		lastConfigMap = lc
		if cfgStr := getMapString(lastConfigMap, "config"); cfgStr != "" {
			iniText = cfgStr
		}
	}

	if iniText == "" {
		if cfgStr := getMapString(item, "config"); cfgStr != "" && strings.Contains(cfgStr, "[Interface]") {
			iniText = cfgStr
		} else if sub != nil {
			if cfgStr := getMapString(sub, "config"); cfgStr != "" && strings.Contains(cfgStr, "[Interface]") {
				iniText = cfgStr
			}
		}
	}

	if iniText == "" {
		privKey := getMapStringFromSources([]map[string]interface{}{lastConfigMap, sub, item}, "PrivateKey", "private_key", "privkey")
		pubKey := getMapStringFromSources([]map[string]interface{}{lastConfigMap, sub, item}, "PublicKey", "public_key", "pubkey")
		addr := getMapStringFromSources([]map[string]interface{}{lastConfigMap, sub, item}, "Address", "address", "client_ip")
		port := getMapIntFromSources([]map[string]interface{}{lastConfigMap, sub, item}, "port", "Port")
		host := getMapStringFromSources([]map[string]interface{}{lastConfigMap, sub, item}, "hostName", "hostname", "server", "endpoint")
		if host == "" {
			host = rootHost
		}
		if privKey != "" && pubKey != "" && addr != "" {
			endpoint := host
			if port > 0 {
				endpoint = net.JoinHostPort(host, strconv.Itoa(port))
			}
			iniText = fmt.Sprintf("[Interface]\nPrivateKey = %s\nAddress = %s\n\n[Peer]\nPublicKey = %s\nEndpoint = %s\nAllowedIPs = 0.0.0.0/0\n",
				privKey, addr, pubKey, endpoint)
		}
	}

	if iniText == "" {
		return Draft{}, fmt.Errorf("amnezia: в контейнере awg/wireguard не найден конфиг")
	}

	awgParams := extractAWGParams(item, sub, lastConfigMap)
	iniText = injectAwgParamsIntoIni(iniText, awgParams)
	iniText = ensureIniEndpointHost(iniText, rootHost, item, sub, lastConfigMap)

	return parseAmneziaWGIni(iniText)
}

func extractAWGParams(sources ...map[string]interface{}) map[string]string {
	keys := []string{
		"Jc", "Jmin", "Jmax",
		"S1", "S2", "S3", "S4",
		"H1", "H2", "H3", "H4",
		"I1", "I2", "I3", "I4", "I5",
		"MTU",
	}
	res := make(map[string]string)
	for _, src := range sources {
		if src == nil {
			continue
		}
		for _, k := range keys {
			val := getAnyString(src, k, strings.ToLower(k))
			if val != "" && res[k] == "" {
				res[k] = val
			}
		}
	}
	return res
}

func injectAwgParamsIntoIni(iniText string, params map[string]string) string {
	if len(params) == 0 {
		return iniText
	}
	iface, _, _ := splitIniSections(iniText)
	if iface == nil {
		iface = map[string]string{}
	}
	lowerIface := map[string]bool{}
	for k := range iface {
		lowerIface[strings.ToLower(k)] = true
	}

	var toAdd []string
	for k, v := range params {
		if !lowerIface[strings.ToLower(k)] && strings.TrimSpace(v) != "" {
			toAdd = append(toAdd, fmt.Sprintf("%s = %s", k, strings.TrimSpace(v)))
		}
	}
	if len(toAdd) == 0 {
		return iniText
	}

	lines := strings.Split(iniText, "\n")
	var out []string
	inserted := false
	for _, line := range lines {
		out = append(out, line)
		trimmed := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(line, "\r")))
		if trimmed == "[interface]" && !inserted {
			out = append(out, toAdd...)
			inserted = true
		}
	}
	if !inserted {
		out = append([]string{"[Interface]"}, append(toAdd, iniText)...)
	}
	return strings.Join(out, "\n")
}

func ensureIniEndpointHost(iniText, rootHost string, sources ...map[string]interface{}) string {
	if rootHost == "" {
		return iniText
	}
	lines := strings.Split(iniText, "\n")
	hasEndpoint := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		eq := strings.Index(trimmed, "=")
		if eq > 0 {
			key := strings.ToLower(strings.TrimSpace(trimmed[:eq]))
			if key == "endpoint" {
				hasEndpoint = true
				val := strings.TrimSpace(trimmed[eq+1:])
				if strings.HasPrefix(val, ":") {
					lines[i] = fmt.Sprintf("Endpoint = %s%s", rootHost, val)
				} else if h, p, err := net.SplitHostPort(val); err == nil && (h == "" || h == "0.0.0.0") {
					lines[i] = fmt.Sprintf("Endpoint = %s:%s", rootHost, p)
				}
			}
		}
	}
	if !hasEndpoint {
		port := getMapIntFromSources(sources, "port", "Port")
		if port > 0 {
			for i, line := range lines {
				if strings.ToLower(strings.TrimSpace(line)) == "[peer]" {
					lines = append(lines[:i+1], append([]string{fmt.Sprintf("Endpoint = %s:%d", rootHost, port)}, lines[i+1:]...)...)
					break
				}
			}
		}
	}
	return strings.Join(lines, "\n")
}

func parseAmneziaContainerXray(item map[string]interface{}, rootHost string) (Draft, error) {
	var sub map[string]interface{}
	if s, ok := item["xray"].(map[string]interface{}); ok {
		sub = s
	}

	lastConfigRaw := item["last_config"]
	if lastConfigRaw == nil && sub != nil {
		lastConfigRaw = sub["last_config"]
	}
	if lastConfigRaw == nil {
		lastConfigRaw = item["lastConfig"]
		if lastConfigRaw == nil && sub != nil {
			lastConfigRaw = sub["lastConfig"]
		}
	}

	var xrayMap map[string]interface{}
	switch lc := lastConfigRaw.(type) {
	case string:
		lc = strings.TrimSpace(lc)
		if strings.HasPrefix(lc, "vless://") {
			u, err := url.Parse(lc)
			if err != nil {
				return Draft{}, fmt.Errorf("amnezia: не удалось разобрать vless URL: %w", err)
			}
			return parseVLESS(u)
		}
		if strings.HasPrefix(lc, "{") {
			_ = json.Unmarshal([]byte(lc), &xrayMap)
		}
	case map[string]interface{}:
		xrayMap = lc
	}

	if xrayMap == nil {
		xrayMap = sub
	}
	if xrayMap == nil {
		xrayMap = item
	}

	return parseXrayMap(xrayMap, rootHost, item, sub)
}

func parseXrayMap(xrayMap map[string]interface{}, rootHost string, item, sub map[string]interface{}) (Draft, error) {
	var server, uuid, flow string
	var port int
	var streamSettings map[string]interface{}

	// 1. Check outbounds (client config format)
	if obs, ok := xrayMap["outbounds"].([]interface{}); ok && len(obs) > 0 {
		for _, ob := range obs {
			if obm, ok := ob.(map[string]interface{}); ok {
				proto := strings.ToLower(getMapString(obm, "protocol", "type"))
				if proto == "vless" {
					if ss, ok := obm["streamSettings"].(map[string]interface{}); ok {
						streamSettings = ss
					}
					if sets, ok := obm["settings"].(map[string]interface{}); ok {
						if vnexts, ok := sets["vnext"].([]interface{}); ok && len(vnexts) > 0 {
							if vn, ok := vnexts[0].(map[string]interface{}); ok {
								server = getMapString(vn, "address")
								port = getMapInt(vn, "port")
								if users, ok := vn["users"].([]interface{}); ok && len(users) > 0 {
									if u, ok := users[0].(map[string]interface{}); ok {
										uuid = getMapString(u, "id")
										flow = getMapString(u, "flow")
									}
								}
							}
						}
					}
					break
				}
			}
		}
	}

	// 2. Check inbounds (Amnezia server template format)
	if uuid == "" {
		if ibs, ok := xrayMap["inbounds"].([]interface{}); ok && len(ibs) > 0 {
			for _, ib := range ibs {
				if ibm, ok := ib.(map[string]interface{}); ok {
					proto := strings.ToLower(getMapString(ibm, "protocol"))
					if proto == "vless" {
						port = getMapInt(ibm, "port")
						if ss, ok := ibm["streamSettings"].(map[string]interface{}); ok {
							streamSettings = ss
						}
						if sets, ok := ibm["settings"].(map[string]interface{}); ok {
							if clients, ok := sets["clients"].([]interface{}); ok && len(clients) > 0 {
								if cl, ok := clients[0].(map[string]interface{}); ok {
									uuid = getMapString(cl, "id")
									flow = getMapString(cl, "flow")
								}
							}
						}
						break
					}
				}
			}
		}
	}

	// 3. Direct fields fallback
	if server == "" {
		server = getMapStringFromSources([]map[string]interface{}{xrayMap, sub, item}, "server", "host", "hostName", "hostname")
		if server == "" {
			server = rootHost
		}
	}
	if port == 0 {
		port = getMapIntFromSources([]map[string]interface{}{xrayMap, sub, item}, "port", "server_port", "Port")
	}
	if uuid == "" {
		uuid = getMapStringFromSources([]map[string]interface{}{xrayMap, sub, item}, "uuid", "id")
	}
	if flow == "" {
		flow = getMapStringFromSources([]map[string]interface{}{xrayMap, sub, item}, "flow")
	}
	if streamSettings == nil {
		if ss, ok := xrayMap["streamSettings"].(map[string]interface{}); ok {
			streamSettings = ss
		}
	}

	if server == "" || port == 0 || uuid == "" {
		return Draft{}, fmt.Errorf("amnezia: неполная конфигурация xray/vless (нужны server, port, uuid)")
	}

	cfg := map[string]interface{}{
		"type":            "vless",
		"server":          server,
		"server_port":     port,
		"uuid":            uuid,
		"packet_encoding": "xudp",
	}
	if flow != "" {
		cfg["flow"] = flow
	}

	var security, sni, fp, pbk, sid string
	if streamSettings != nil {
		security = strings.ToLower(getMapString(streamSettings, "security"))
		if rs, ok := streamSettings["realitySettings"].(map[string]interface{}); ok {
			pbk = getMapString(rs, "publicKey", "public_key", "pbk")
			fp = getMapString(rs, "fingerprint", "fp")
			sid = getMapString(rs, "shortId", "short_id", "sid")
			if sid == "" {
				if sids, ok := rs["shortIds"].([]interface{}); ok && len(sids) > 0 {
					sid = fmt.Sprint(sids[0])
				}
			}
			sni = getMapString(rs, "serverName", "server_name", "sni")
			if sni == "" {
				if sns, ok := rs["serverNames"].([]interface{}); ok && len(sns) > 0 {
					sni = fmt.Sprint(sns[0])
				}
			}
			if sni == "" {
				dest := getMapString(rs, "dest")
				if h, _, err := net.SplitHostPort(dest); err == nil && h != "" {
					sni = h
				} else if dest != "" {
					sni = dest
				}
			}
		}
		if tlsSettings, ok := streamSettings["tlsSettings"].(map[string]interface{}); ok {
			if sni == "" {
				sni = getMapString(tlsSettings, "serverName", "server_name", "sni")
			}
			if fp == "" {
				fp = getMapString(tlsSettings, "fingerprint", "fp")
			}
		}
		if fp == "" {
			if utls, ok := streamSettings["utls"].(map[string]interface{}); ok {
				fp = getMapString(utls, "fingerprint", "fp")
			}
		}
	}
	if sni == "" {
		sni = server
	}
	if fp == "" && security == "reality" {
		fp = "chrome"
	}

	if security == "reality" {
		tls := map[string]interface{}{
			"enabled":     true,
			"server_name": sni,
			"utls": map[string]interface{}{
				"enabled":     true,
				"fingerprint": fp,
			},
			"reality": map[string]interface{}{
				"enabled":    true,
				"public_key": pbk,
			},
		}
		if sid != "" {
			tls["reality"].(map[string]interface{})["short_id"] = sid
		}
		cfg["tls"] = tls
	} else if security == "tls" {
		tls := map[string]interface{}{
			"enabled":     true,
			"server_name": sni,
		}
		if fp != "" {
			tls["utls"] = map[string]interface{}{
				"enabled":     true,
				"fingerprint": fp,
			}
		}
		cfg["tls"] = tls
	}

	if streamSettings != nil {
		network := strings.ToLower(getMapString(streamSettings, "network"))
		switch network {
		case "ws", "websocket":
			tr := map[string]interface{}{"type": "ws"}
			if ws, ok := streamSettings["wsSettings"].(map[string]interface{}); ok {
				if p := getMapString(ws, "path"); p != "" {
					tr["path"] = p
				}
				if h := getMapString(ws, "headers.Host", "host"); h != "" {
					tr["headers"] = map[string]interface{}{"Host": h}
				}
			}
			cfg["transport"] = tr
		case "grpc":
			tr := map[string]interface{}{"type": "grpc"}
			if gr, ok := streamSettings["grpcSettings"].(map[string]interface{}); ok {
				if s := getMapString(gr, "serviceName", "service_name"); s != "" {
					tr["service_name"] = s
				}
			}
			cfg["transport"] = tr
		case "http", "h2":
			tr := map[string]interface{}{"type": "http"}
			if ht, ok := streamSettings["httpSettings"].(map[string]interface{}); ok {
				if p := getMapString(ht, "path"); p != "" {
					tr["path"] = p
				}
				if h := getMapString(ht, "host"); h != "" {
					tr["host"] = []interface{}{h}
				}
			}
			cfg["transport"] = tr
		case "httpupgrade":
			tr := map[string]interface{}{"type": "httpupgrade"}
			if hu, ok := streamSettings["httpupgradeSettings"].(map[string]interface{}); ok {
				if p := getMapString(hu, "path"); p != "" {
					tr["path"] = p
				}
				if h := getMapString(hu, "host"); h != "" {
					tr["host"] = h
				}
			}
			cfg["transport"] = tr
		case "xhttp", "splithttp":
			tr := map[string]interface{}{"type": "xhttp", "mode": "auto"}
			if xh, ok := streamSettings["xhttpSettings"].(map[string]interface{}); ok {
				if p := getMapString(xh, "path"); p != "" {
					tr["path"] = p
				}
				if h := getMapString(xh, "host"); h != "" {
					tr["host"] = h
				}
				if m := getMapString(xh, "mode"); m != "" {
					tr["mode"] = m
				}
				if extra, ok := xh["extra"].(map[string]interface{}); ok {
					if raw, err := json.Marshal(extra); err == nil {
						applyXHTTPExtra(tr, string(raw))
					}
				}
			}
			cfg["transport"] = tr
		}
	}

	return Draft{Kind: profile.KindOutbound, Label: server, Config: cfg}, nil
}

func parseAmneziaContainerShadowsocks(item map[string]interface{}, rootHost string) (Draft, error) {
	var sub map[string]interface{}
	if s, ok := item["shadowsocks"].(map[string]interface{}); ok {
		sub = s
	}

	lastConfigRaw := item["last_config"]
	if lastConfigRaw == nil && sub != nil {
		lastConfigRaw = sub["last_config"]
	}
	if lastConfigRaw == nil {
		lastConfigRaw = item["lastConfig"]
		if lastConfigRaw == nil && sub != nil {
			lastConfigRaw = sub["lastConfig"]
		}
	}

	var ssMap map[string]interface{}
	switch lc := lastConfigRaw.(type) {
	case string:
		lc = strings.TrimSpace(lc)
		if strings.HasPrefix(lc, "ss://") {
			u, err := url.Parse(lc)
			if err != nil {
				return Draft{}, fmt.Errorf("amnezia: не удалось разобрать shadowsocks URL: %w", err)
			}
			return parseShadowsocks(lc, u)
		}
		if strings.HasPrefix(lc, "{") {
			_ = json.Unmarshal([]byte(lc), &ssMap)
		}
	case map[string]interface{}:
		ssMap = lc
	}

	sources := []map[string]interface{}{ssMap, sub, item}
	server := getMapStringFromSources(sources, "server", "host", "hostName", "hostname")
	if server == "" {
		server = rootHost
	}
	port := getMapIntFromSources(sources, "server_port", "port", "Port")
	method := getMapStringFromSources(sources, "method", "cipher")
	password := getMapStringFromSources(sources, "password")

	if server == "" || port == 0 || method == "" || password == "" {
		return Draft{}, fmt.Errorf("amnezia: неполная конфигурация shadowsocks (нужны server, port, method, password)")
	}

	cfg := map[string]interface{}{
		"type":        "shadowsocks",
		"server":      server,
		"server_port": port,
		"method":      method,
		"password":    password,
	}
	return Draft{Kind: profile.KindOutbound, Label: server, Config: cfg}, nil
}

func getMapString(m map[string]interface{}, keys ...string) string {
	if m == nil {
		return ""
	}
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			switch s := v.(type) {
			case string:
				if strings.TrimSpace(s) != "" {
					return strings.TrimSpace(s)
				}
			case fmt.Stringer:
				if strings.TrimSpace(s.String()) != "" {
					return strings.TrimSpace(s.String())
				}
			}
		}
	}
	return ""
}

func getMapInt(m map[string]interface{}, keys ...string) int {
	if m == nil {
		return 0
	}
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			switch n := v.(type) {
			case float64:
				return int(n)
			case int:
				return n
			case int64:
				return int(n)
			case string:
				if i, err := strconv.Atoi(strings.TrimSpace(n)); err == nil {
					return i
				}
			}
		}
	}
	return 0
}

func getMapStringFromSources(sources []map[string]interface{}, keys ...string) string {
	for _, src := range sources {
		if src == nil {
			continue
		}
		if val := getMapString(src, keys...); val != "" {
			return val
		}
	}
	return ""
}

func getMapIntFromSources(sources []map[string]interface{}, keys ...string) int {
	for _, src := range sources {
		if src == nil {
			continue
		}
		if val := getMapInt(src, keys...); val != 0 {
			return val
		}
	}
	return 0
}

func getAnyString(m map[string]interface{}, keys ...string) string {
	if m == nil {
		return ""
	}
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			switch s := v.(type) {
			case string:
				if strings.TrimSpace(s) != "" {
					return strings.TrimSpace(s)
				}
			case float64:
				return strconv.Itoa(int(s))
			case int:
				return strconv.Itoa(s)
			case int64:
				return strconv.FormatInt(s, 10)
			}
		}
	}
	return ""
}
