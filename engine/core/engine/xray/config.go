// Package xray adapts the common client contract to the standalone Xray core.
package xray

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"NekkoVPN/core/profile"
	"NekkoVPN/core/subscription"
)

const Version = "26.10.21"

type object = map[string]interface{}

// Decode validates one protocol link and retains its original portable form.
// Provider HTTP and subscription envelopes are handled by the client.
func Decode(input string) ([]profile.Profile, error) {
	input = subscription.NormalizeLink(input)
	u, err := url.Parse(input)
	if err != nil {
		return nil, fmt.Errorf("Xray: неверная ссылка")
	}
	if _, err := outbound(input, "validate"); err != nil {
		return nil, err
	}
	port, _ := strconv.Atoi(u.Port())
	protocol := strings.ToLower(u.Scheme)
	if protocol == "hy2" {
		protocol = "hysteria2"
	}
	if protocol == "ss" {
		protocol = "shadowsocks"
	}
	// SIP002's base64 userinfo is still parsed as a normal URL host.
	label := u.Fragment
	if label == "" {
		label = u.Hostname()
	}
	return []profile.Profile{{Label: label, Connection: &profile.Connection{URI: input, Protocol: protocol, Host: u.Hostname(), Port: port}}}, nil
}

// Compile emits one SOCKS listener and transport outbound per opaque profile ID.
// There is no direct fallback: unknown protocols/parameters fail compilation.
func Compile(profiles []profile.Profile, ports map[string]int) (object, error) {
	inbounds := []interface{}{}
	outbounds := []interface{}{}
	rules := []interface{}{}
	seen := map[string]bool{}
	for i, p := range profiles {
		if seen[p.Name] {
			return nil, fmt.Errorf("Xray: повторяющийся ID профиля")
		}
		seen[p.Name] = true
		if p.Connection == nil {
			return nil, fmt.Errorf("Xray: профиль %q требует переносимого описания подключения; требуется миграция сохранённого профиля", p.Label)
		}
		if err := validateLegacy(p); err != nil {
			return nil, err
		}
		port := ports[p.Name]
		if port < 1 || port > 65535 {
			return nil, fmt.Errorf("Xray: не выделен локальный порт профиля %q", p.Label)
		}
		tag := fmt.Sprintf("transport-%d", i)
		ob, err := outbound(p.Connection.URI, tag)
		if err != nil {
			return nil, fmt.Errorf("профиль %q: %w", p.Label, err)
		}
		in := fmt.Sprintf("socks-%d", i)
		inbounds = append(inbounds, object{"tag": in, "listen": "127.0.0.1", "port": port, "protocol": "socks", "settings": object{"auth": "noauth", "udp": true, "ip": "127.0.0.1"}})
		outbounds = append(outbounds, ob)
		rules = append(rules, object{"type": "field", "inboundTag": []string{in}, "outboundTag": tag})
	}
	// Unknown inbound traffic cannot escape through the first real server.
	outbounds = append([]interface{}{object{"tag": "reject", "protocol": "blackhole"}}, outbounds...)
	return object{"log": object{"loglevel": "warning"}, "inbounds": inbounds, "outbounds": outbounds, "routing": object{"domainStrategy": "AsIs", "rules": rules}}, nil
}

func outbound(input, tag string) (object, error) {
	u, err := url.Parse(input)
	if err != nil {
		return nil, fmt.Errorf("Xray: неверная ссылка")
	}
	proto := strings.ToLower(u.Scheme)
	switch proto {
	case "vless", "trojan", "ss", "hysteria2", "hy2":
	default:
		return nil, fmt.Errorf("Xray %s: протокол %q не поддержан этим адаптером", Version, proto)
	}
	if u.User == nil || u.User.Username() == "" {
		return nil, fmt.Errorf("Xray: в ссылке нет учётных данных")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 || u.Hostname() == "" {
		return nil, fmt.Errorf("Xray: требуется корректный host:port")
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, fmt.Errorf("Xray: неверные параметры ссылки")
	}
	supported := map[string]bool{}
	for _, k := range strings.Fields("security type sni peer fp pbk sid spx flow encryption alpn host path serviceName authority mode extra headerType packetEncoding obfs obfs-password pinSHA256 finalmask hikkari-node") {
		supported[k] = true
	}
	for k, v := range q {
		if !supported[k] || len(v) != 1 {
			return nil, fmt.Errorf("Xray: параметр %q не поддержан этим адаптером", k)
		}
	}
	ob := object{"tag": tag, "protocol": proto}
	credential := u.User.Username()
	if pw, ok := u.User.Password(); ok && proto != "ss" {
		credential += ":" + pw
	}
	switch proto {
	case "vless":
		user := object{"id": credential, "encryption": "none"}
		if e := q.Get("encryption"); e != "" && e != "none" {
			return nil, fmt.Errorf("Xray: VLESS encryption=%q пока не поддержан переносимым адаптером", e)
		}
		if flow := q.Get("flow"); flow != "" {
			user["flow"] = flow
		}
		if pe := q.Get("packetEncoding"); pe != "" && pe != "xudp" {
			return nil, fmt.Errorf("Xray: packetEncoding=%q не поддержан", pe)
		}
		ob["settings"] = object{"vnext": []interface{}{object{"address": u.Hostname(), "port": port, "users": []interface{}{user}}}}
	case "trojan":
		ob["settings"] = object{"servers": []interface{}{object{"address": u.Hostname(), "port": port, "password": credential}}}
	case "ss":
		method, password, ok := strings.Cut(credential, ":")
		if pw, yes := u.User.Password(); yes {
			method, password, ok = credential, pw, true
		}
		if !ok {
			for _, enc := range []*base64.Encoding{base64.RawURLEncoding, base64.RawStdEncoding, base64.StdEncoding, base64.URLEncoding} {
				if b, e := enc.DecodeString(credential); e == nil {
					method, password, ok = strings.Cut(string(b), ":")
					if ok {
						break
					}
				}
			}
		}
		if !ok || method == "" || password == "" {
			return nil, fmt.Errorf("Xray: неверные Shadowsocks credentials")
		}
		ob["protocol"] = "shadowsocks"
		ob["settings"] = object{"servers": []interface{}{object{"address": u.Hostname(), "port": port, "method": method, "password": password}}}
	case "hy2", "hysteria2":
		ob["protocol"] = "hysteria"
		ob["settings"] = object{"version": 2, "address": u.Hostname(), "port": port}
	}
	stream, err := streamSettings(q, u.Hostname(), proto, credential)
	if err != nil {
		return nil, err
	}
	ob["streamSettings"] = stream
	return ob, nil
}

func streamSettings(q url.Values, host, proto, credential string) (object, error) {
	network := q.Get("type")
	if network == "" {
		network = "tcp"
	}
	if network == "splithttp" {
		network = "xhttp"
	}
	security := q.Get("security")
	if security == "" {
		security = "none"
		if proto == "trojan" || proto == "hy2" || proto == "hysteria2" {
			security = "tls"
		}
	}
	if proto == "hy2" || proto == "hysteria2" {
		network = "hysteria"
		if security != "tls" {
			return nil, fmt.Errorf("Xray: Hysteria2 требует TLS")
		}
	}
	s := object{"network": network, "security": security}
	name := q.Get("sni")
	if name == "" {
		name = q.Get("peer")
	}
	if name == "" {
		name = host
	}
	switch security {
	case "none":
		for _, k := range []string{"sni", "peer", "fp", "pbk", "sid", "spx", "alpn", "pinSHA256"} {
			if q.Get(k) != "" {
				return nil, fmt.Errorf("Xray: %s требует TLS/Reality", k)
			}
		}
	case "tls":
		tls := object{"serverName": name}
		if fp := q.Get("fp"); fp != "" {
			tls["fingerprint"] = fp
		}
		if alpn := q.Get("alpn"); alpn != "" {
			tls["alpn"] = strings.Split(alpn, ",")
		}
		if pin := q.Get("pinSHA256"); pin != "" {
			normalized, err := certificatePin(pin)
			if err != nil {
				return nil, err
			}
			tls["pinnedPeerCertSha256"] = normalized
		}
		for _, k := range []string{"pbk", "sid", "spx"} {
			if q.Get(k) != "" {
				return nil, fmt.Errorf("Xray: %s требует Reality", k)
			}
		}
		s["tlsSettings"] = tls
	case "reality":
		if q.Get("pbk") == "" {
			return nil, fmt.Errorf("Xray Reality: требуется pbk")
		}
		fp := q.Get("fp")
		if fp == "" {
			fp = "chrome"
		}
		s["realitySettings"] = object{"serverName": name, "fingerprint": fp, "publicKey": q.Get("pbk"), "shortId": q.Get("sid"), "spiderX": q.Get("spx")}
		if q.Get("alpn") != "" || q.Get("pinSHA256") != "" {
			return nil, fmt.Errorf("Xray Reality: alpn/pinSHA256 не поддержаны")
		}
	default:
		return nil, fmt.Errorf("Xray: security=%q не поддержан", security)
	}
	switch network {
	case "tcp", "raw":
		if h := q.Get("headerType"); h != "" && h != "none" {
			return nil, fmt.Errorf("Xray: TCP headerType=%q не поддержан", h)
		}
		if q.Get("host") != "" || q.Get("path") != "" {
			return nil, fmt.Errorf("Xray: host/path требуют HTTP-транспорт")
		}
	case "ws":
		ws := object{"path": q.Get("path")}
		if h := q.Get("host"); h != "" {
			ws["headers"] = object{"Host": h}
		}
		s["wsSettings"] = ws
	case "grpc":
		if q.Get("host") != "" || q.Get("path") != "" {
			return nil, fmt.Errorf("Xray: gRPC использует serviceName/authority вместо host/path")
		}
		if mode := q.Get("mode"); mode != "" && mode != "gun" && mode != "multi" {
			return nil, fmt.Errorf("Xray: неверный режим gRPC")
		}
		s["grpcSettings"] = object{"serviceName": q.Get("serviceName"), "authority": q.Get("authority"), "multiMode": q.Get("mode") == "multi"}
	case "xhttp":
		mode := q.Get("mode")
		if mode == "" {
			mode = "auto"
		}
		switch mode {
		case "auto", "packet-up", "stream-up", "stream-one":
		default:
			return nil, fmt.Errorf("Xray: неверный режим XHTTP")
		}
		tr := object{"host": q.Get("host"), "path": q.Get("path"), "mode": mode}
		if extra := q.Get("extra"); extra != "" {
			var value object
			if json.Unmarshal([]byte(extra), &value) != nil || value == nil {
				return nil, fmt.Errorf("Xray: extra должен быть JSON-объектом")
			}
			tr["extra"] = value
		}
		s["xhttpSettings"] = tr
	case "hysteria":
		if proto != "hy2" && proto != "hysteria2" {
			return nil, fmt.Errorf("Xray: hysteria transport требует Hysteria2-ссылку")
		}
		if q.Get("obfs-password") != "" && q.Get("obfs") == "" {
			return nil, fmt.Errorf("Xray: obfs-password требует obfs")
		}
		if q.Get("host") != "" || q.Get("path") != "" {
			return nil, fmt.Errorf("Xray: Hysteria2 не использует host/path")
		}
		s["hysteriaSettings"] = object{"version": 2, "auth": credential}
		if encoded := q.Get("finalmask"); encoded != "" {
			if q.Get("pinSHA256") == "" || q.Get("obfs") != "" || q.Get("obfs-password") != "" {
				return nil, fmt.Errorf("Xray: finalmask требует pinSHA256 и не совмещается с obfs")
			}
			masks, err := packetMasks(encoded)
			if err != nil {
				return nil, err
			}
			s["finalmask"] = object{"udp": masks}
		}

		if obfs := q.Get("obfs"); obfs != "" {
			if obfs != "salamander" || q.Get("obfs-password") == "" {
				return nil, fmt.Errorf("Xray: поддержана только Salamander с паролем")
			}
			s["finalmask"] = object{"udp": []interface{}{object{"type": "salamander", "settings": object{"password": q.Get("obfs-password")}}}}
		}
	default:
		return nil, fmt.Errorf("Xray: транспорт %q не поддержан этим адаптером", network)
	}
	// Reject parameters that would otherwise be accepted but ignored.
	if network != "tcp" && network != "raw" && q.Get("headerType") != "" && q.Get("headerType") != "none" {
		return nil, fmt.Errorf("Xray: headerType относится к TCP")
	}
	if network == "ws" && q.Get("mode") != "" {
		return nil, fmt.Errorf("Xray: mode не поддержан для WS")
	}
	for _, k := range []string{"serviceName", "authority"} {
		if q.Get(k) != "" && network != "grpc" {
			return nil, fmt.Errorf("Xray: %s требует gRPC", k)
		}
	}
	if q.Get("extra") != "" && network != "xhttp" {
		return nil, fmt.Errorf("Xray: extra требует XHTTP")
	}
	if q.Get("mode") != "" && network != "xhttp" && network != "grpc" {
		return nil, fmt.Errorf("Xray: mode требует XHTTP/gRPC")
	}
	if q.Get("finalmask") != "" && network != "hysteria" {
		return nil, fmt.Errorf("Xray: finalmask требует Hysteria2")
	}
	if (q.Get("obfs") != "" || q.Get("obfs-password") != "") && network != "hysteria" {
		return nil, fmt.Errorf("Xray: obfs требует Hysteria2")
	}
	if proto != "vless" && (q.Get("flow") != "" || q.Get("encryption") != "" || q.Get("packetEncoding") != "") {
		return nil, fmt.Errorf("Xray: параметры VLESS в другом протоколе")
	}
	return s, nil
}

// AllocatePorts retains listener identity while the existing process is running.
func AllocatePorts(profiles []profile.Profile, previous map[string]int) (map[string]int, error) {
	ports := make(map[string]int, len(profiles))
	reserved := []net.Listener{}
	defer func() {
		for _, l := range reserved {
			_ = l.Close()
		}
	}()
	used := map[int]bool{}
	for _, p := range profiles {
		if port := previous[p.Name]; port != 0 {
			ports[p.Name] = port
			used[port] = true
		}
	}
	for _, p := range profiles {
		if ports[p.Name] != 0 {
			continue
		}
		for {
			l, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				return nil, err
			}
			reserved = append(reserved, l)
			port := l.Addr().(*net.TCPAddr).Port
			if used[port] {
				continue
			}
			used[port] = true
			ports[p.Name] = port
			break
		}
	}
	return ports, nil
}

// Unknown native options cannot be silently discarded when changing transport.
// The saved map stays untouched so returning to sing-box remains lossless.
func validateLegacy(p profile.Profile) error {
	if p.Config == nil {
		return nil
	}
	allowed := map[string]bool{}
	for _, key := range strings.Fields("type tag server server_port uuid flow packet_encoding password method tls transport") {
		allowed[key] = true
	}
	for key := range p.Config {
		if !allowed[key] {
			return fmt.Errorf("Xray: параметр sing-box %q профиля %q не переносится", key, p.Label)
		}
	}
	for section, keys := range map[string]string{"tls": "enabled server_name alpn utls reality insecure", "transport": "type path host headers service_name mode extra"} {
		native, ok := p.Config[section].(map[string]interface{})
		if !ok {
			continue
		}
		supported := map[string]bool{}
		for _, key := range strings.Fields(keys) {
			supported[key] = true
		}
		for key := range native {
			if !supported[key] {
				return fmt.Errorf("Xray: параметр sing-box %s.%s профиля %q не переносится", section, key, p.Label)
			}
		}
	}
	for _, section := range []struct{ parent, key, allowed string }{
		{"tls", "utls", "enabled fingerprint"},
		{"tls", "reality", "enabled public_key short_id"},
		{"transport", "headers", "Host"},
	} {
		parent, _ := p.Config[section.parent].(map[string]interface{})
		value, exists := parent[section.key]
		if !exists {
			continue
		}
		fields, ok := value.(map[string]interface{})
		if !ok {
			return fmt.Errorf("Xray: некорректный legacy-параметр %s.%s", section.parent, section.key)
		}
		allowed := map[string]bool{}
		for _, key := range strings.Fields(section.allowed) {
			allowed[key] = true
		}
		for key := range fields {
			if !allowed[key] {
				return fmt.Errorf("Xray: параметр sing-box %s.%s.%s профиля %q не переносится", section.parent, section.key, key, p.Label)
			}
		}
	}

	return nil
}
