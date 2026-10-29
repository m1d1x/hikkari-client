// URI parsers for "import-by-link" profiles. ParseURI dispatches on the URI
// scheme and returns a profile draft (kind + label + sing-box config block)
// ready to hand to Store.Add. Add new schemes by appending a case to the
// switch and a parser function below.
package profile

import (
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Draft is the parsed result of an import URI. Kind/Config are what
// Store.Add wants; Label is the human-readable name taken from the URI
// fragment (or falls back to host).
type Draft struct {
	Kind   string
	Label  string
	Config map[string]interface{}
}

// ParseURI dispatches by scheme. Returns an error for unknown schemes so the
// UI can show "неподдерживаемая ссылка" rather than silently failing.
func ParseURI(uri string) (Draft, error) {
	uri = normalizeImportLink(uri)
	if uri == "" {
		return Draft{}, fmt.Errorf("пустая ссылка")
	}
	if looksLikeWireGuardIni(uri) {
		return parseAmneziaWGIni(uri)
	}
	u, err := url.Parse(uri)
	if err != nil {
		return Draft{}, fmt.Errorf("не удалось разобрать ссылку: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		drafts, err := fetchSubscription(uri)
		if err != nil {
			return Draft{}, err
		}
		if len(drafts) == 0 {
			return Draft{}, fmt.Errorf("подписка пуста")
		}
		return drafts[0], nil
	case "hysteria2", "hy2":
		return parseHysteria2(u)
	case "vless":
		return parseVLESS(u)
	case "trojan":
		return parseTrojan(u)
	case "ss":
		return parseShadowsocks(uri, u)
	}
	return Draft{}, fmt.Errorf("неподдерживаемая схема: %s", u.Scheme)
}

// ParseURIs parses either a single URI, a multi-line string of URIs,
// or fetches an http/https subscription URL and parses all URIs in it.
func ParseURIs(input string) ([]Draft, error) {
	return parseImport(input, true)
}

// normalizeImportLink accepts links copied from chat messages as well as raw
// clipboard text. Only strip balanced wrappers; URL query/fragment data stays
// untouched.
func normalizeImportLink(input string) string {
	input = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(input), "\ufeff"))
	if strings.Contains(input, "\n") {
		return input
	}
	for _, pair := range [][2]string{{"\"", "\""}, {"'", "'"}, {"`", "`"}, {"<", ">"}, {"“", "”"}, {"«", "»"}} {
		if strings.HasPrefix(input, pair[0]) && strings.HasSuffix(input, pair[1]) && len(input) >= len(pair[0])+len(pair[1]) {
			input = strings.TrimSpace(input[len(pair[0]) : len(input)-len(pair[1])])
		}
	}
	if strings.HasPrefix(input, "[") && strings.HasSuffix(input, ")") {
		if i := strings.Index(input, "]("); i >= 0 {
			input = strings.TrimSpace(input[i+2 : len(input)-1])
		}
	}
	return input
}

func isSubscriptionURL(input string) bool {
	lower := strings.ToLower(input)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func parseImport(input string, allowSubscriptions bool) ([]Draft, error) {
	input = normalizeImportLink(input)
	if input == "" {
		return nil, fmt.Errorf("пустая ссылка")
	}
	if looksLikeWireGuardIni(input) {
		d, err := parseAmneziaWGIni(input)
		if err != nil {
			return nil, err
		}
		return []Draft{d}, nil
	}
	// Providers can wrap base64 over several lines, including CRLF.
	if decoded, err := decodeBase64Subscription(input); err == nil {
		input = decoded
	}

	var drafts []Draft
	var firstErr error
	for _, line := range strings.Split(input, "\n") {
		line = normalizeImportLink(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		if isSubscriptionURL(line) {
			// Expand every subscription in a pasted list. ParseURI only returns
			// one draft, which used to silently discard the remaining servers.
			if !allowSubscriptions {
				return nil, fmt.Errorf("подписка содержит другую HTTP-ссылку вместо серверов")
			}
			servers, err := fetchSubscription(line)
			if err != nil {
				return nil, err
			}
			drafts = append(drafts, servers...)
			continue
		}
		d, err := ParseURI(line)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		drafts = append(drafts, d)
	}
	if len(drafts) == 0 && firstErr != nil {
		return nil, firstErr
	}
	if len(drafts) == 0 {
		return nil, fmt.Errorf("в ссылке не найдено поддерживаемых серверов")
	}
	return drafts, nil
}

func fetchSubscription(subURL string) ([]Draft, error) {
	client := &http.Client{
		Timeout: 15 * time.Second,
	}
	req, err := http.NewRequest(http.MethodGet, subURL, nil)
	if err != nil {
		return nil, fmt.Errorf("ошибка создания запроса подписки: %w", err)
	}
	req.Header.Set("User-Agent", "v2rayN/6.42 Hikkari/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("не удалось загрузить подписку: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("подписка вернула HTTP %d", resp.StatusCode)
	}

	const maxSubscriptionBytes = 2 * 1024 * 1024
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSubscriptionBytes+1))
	if err != nil {
		return nil, fmt.Errorf("ошибка чтения подписки: %w", err)
	}
	if len(body) > maxSubscriptionBytes {
		return nil, fmt.Errorf("подписка превышает 2 МиБ")
	}
	// Do not follow URLs inside downloaded content: a subscription must
	// contain server URIs, otherwise providers could form an infinite cycle.
	drafts, err := parseImport(string(body), false)
	if err != nil {
		return nil, fmt.Errorf("не удалось разобрать подписку: %w", err)
	}
	return drafts, nil
}

func decodeBase64Subscription(s string) (string, error) {
	s = strings.TrimSpace(s)
	clean := strings.ReplaceAll(strings.ReplaceAll(s, "\r", ""), "\n", "")
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.RawURLEncoding} {
		data, err := enc.DecodeString(clean)
		if err == nil && len(data) > 0 {
			res := string(data)
			if strings.Contains(res, "://") {
				return res, nil
			}
		}
	}
	return "", fmt.Errorf("not base64")
}

// parseHysteria2 maps a hysteria2:// URI to the sing-box outbound shape.
// Reference: https://v2.hysteria.network/docs/developers/URI-Scheme/
func parseHysteria2(u *url.URL) (Draft, error) {
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

	return Draft{Kind: KindOutbound, Label: label, Config: cfg}, nil
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
	return Draft{Kind: KindOutbound, Label: labelOrHost(u, host), Config: cfg}, nil
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
	return Draft{Kind: KindOutbound, Label: labelOrHost(u, host), Config: cfg}, nil
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
	return Draft{Kind: KindOutbound, Label: labelOrHost(u, host), Config: cfg}, nil
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
		return tr
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
	if jc := iface["Jc"]; jc != "" {
		cfg["type"] = "awg"
		if n, err := strconv.Atoi(jc); err == nil {
			cfg["jc"] = n
		}
		if n, err := strconv.Atoi(iface["Jmin"]); err == nil {
			cfg["jmin"] = n
		}
		if n, err := strconv.Atoi(iface["Jmax"]); err == nil {
			cfg["jmax"] = n
		}
		for _, key := range []string{"S1", "S2", "S3", "S4"} {
			if v := iface[key]; v != "" {
				if n, err := strconv.Atoi(v); err == nil {
					cfg[strings.ToLower(key)] = n
				}
			}
		}
		// H1-H4 and I1-I5 are opaque tokens (plain numbers or "n-n" ranges,
		// or magic-header specs for I*) — pass through as strings verbatim.
		for _, key := range []string{"H1", "H2", "H3", "H4", "I1", "I2", "I3", "I4", "I5"} {
			if v := iface[key]; v != "" {
				cfg[strings.ToLower(key)] = v
			}
		}
	}

	label := host
	return Draft{Kind: KindEndpoint, Label: label, Config: cfg}, nil
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
