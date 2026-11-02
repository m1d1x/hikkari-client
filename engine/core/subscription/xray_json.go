package subscription

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Each complete Xray config is one server choice for the user, so it becomes
// one link.

type xrayConfig struct {
	Remarks   string            `json:"remarks"`
	Outbounds []json.RawMessage `json:"outbounds"`
}

type xrayOutbound struct {
	Protocol string `json:"protocol"`
	Settings struct {
		Address  string `json:"address"`
		Port     int    `json:"port"`
		ID       string `json:"id"`
		Flow     string `json:"flow"`
		Password string `json:"password"`
		Method   string `json:"method"`
		Vnext    []struct {
			Address string `json:"address"`
			Port    int    `json:"port"`
			Users   []struct {
				ID       string `json:"id"`
				Flow     string `json:"flow"`
				AlterID  int    `json:"alterId"`
				Security string `json:"security"`
			} `json:"users"`
		} `json:"vnext"`
		Servers []struct {
			Address  string `json:"address"`
			Port     int    `json:"port"`
			Password string `json:"password"`
			Method   string `json:"method"`
		} `json:"servers"`
	} `json:"settings"`
	Stream struct {
		Network  string `json:"network"`
		Security string `json:"security"`
		TLS      struct {
			ServerName    string   `json:"serverName"`
			Fingerprint   string   `json:"fingerprint"`
			ALPN          []string `json:"alpn"`
			AllowInsecure bool     `json:"allowInsecure"`
			PinnedCert    string   `json:"pinnedPeerCertSha256"`
		} `json:"tlsSettings"`
		Reality struct {
			ServerName  string `json:"serverName"`
			Fingerprint string `json:"fingerprint"`
			PublicKey   string `json:"publicKey"`
			ShortID     string `json:"shortId"`
		} `json:"realitySettings"`
		WS struct {
			Path    string            `json:"path"`
			Host    string            `json:"host"`
			Headers map[string]string `json:"headers"`
		} `json:"wsSettings"`
		GRPC struct {
			ServiceName string `json:"serviceName"`
		} `json:"grpcSettings"`
		HTTPUpgrade struct {
			Path string `json:"path"`
			Host string `json:"host"`
		} `json:"httpupgradeSettings"`
		XHTTP struct {
			Path  string          `json:"path"`
			Host  string          `json:"host"`
			Mode  string          `json:"mode"`
			Extra json.RawMessage `json:"extra"`
		} `json:"xhttpSettings"`
		Hysteria struct {
			Auth string `json:"auth"`
		} `json:"hysteriaSettings"`
	} `json:"streamSettings"`
}

// expandXrayConfigs returns the links held by Xray JSON configs. ok is false
// when the input is not such a document. A config with nothing this client can
// dial is left out, which gives the same result on every sync.
func expandXrayConfigs(input string) (links string, ok bool) {
	input = strings.TrimSpace(input)
	var configs []xrayConfig
	switch {
	case strings.HasPrefix(input, "["):
		if json.Unmarshal([]byte(input), &configs) != nil {
			return "", false
		}
	case strings.HasPrefix(input, "{"):
		var config xrayConfig
		if json.Unmarshal([]byte(input), &config) != nil {
			return "", false
		}
		configs = []xrayConfig{config}
	default:
		return "", false
	}
	var out []string
	for _, config := range configs {
		if len(config.Outbounds) == 0 {
			return "", false
		}
		if link := xrayConfigLink(config); link != "" {
			out = append(out, link)
		}
	}
	return strings.Join(out, "\n"), true
}

func xrayConfigLink(config xrayConfig) string {
	for _, raw := range config.Outbounds {
		var outbound xrayOutbound
		if json.Unmarshal(raw, &outbound) != nil {
			continue
		}
		if link, err := xrayOutboundLink(outbound, config.Remarks); err == nil {
			return link
		}
	}
	return ""
}

func xrayOutboundLink(o xrayOutbound, remarks string) (string, error) {
	u := url.URL{Fragment: remarks}
	q := url.Values{}
	var host string
	var port int
	switch strings.ToLower(o.Protocol) {
	case "vless":
		id, flow := o.Settings.ID, o.Settings.Flow
		host, port = o.Settings.Address, o.Settings.Port
		if len(o.Settings.Vnext) > 0 && len(o.Settings.Vnext[0].Users) > 0 {
			next := o.Settings.Vnext[0]
			host, port, id, flow = next.Address, next.Port, next.Users[0].ID, next.Users[0].Flow
		}
		if id == "" {
			return "", fmt.Errorf("vless: нет UUID")
		}
		u.Scheme, u.User = "vless", url.User(id)
		if flow != "" {
			q.Set("flow", flow)
		}
		xrayStreamQuery(o, q)
	case "vmess":
		if len(o.Settings.Vnext) == 0 || len(o.Settings.Vnext[0].Users) == 0 {
			return "", fmt.Errorf("vmess: нет UUID")
		}
		next := o.Settings.Vnext[0]
		user := next.Users[0]
		if next.Address == "" || next.Port <= 0 {
			return "", fmt.Errorf("vmess: не указан host:port")
		}
		xrayStreamQuery(o, q)
		return serverLink(server{
			protocol: "vmess", host: next.Address, port: strconv.Itoa(next.Port), label: remarks,
			uuid: user.ID, cipher: user.Security, alterID: strconv.Itoa(user.AlterID),
		}, q)
	case "trojan":
		if len(o.Settings.Servers) == 0 || o.Settings.Servers[0].Password == "" {
			return "", fmt.Errorf("trojan: нет пароля")
		}
		server := o.Settings.Servers[0]
		host, port = server.Address, server.Port
		u.Scheme, u.User = "trojan", url.User(server.Password)
		xrayStreamQuery(o, q)
	case "shadowsocks":
		if len(o.Settings.Servers) == 0 || o.Settings.Servers[0].Method == "" {
			return "", fmt.Errorf("shadowsocks: нет метода")
		}
		server := o.Settings.Servers[0]
		host, port = server.Address, server.Port
		creds := base64.RawURLEncoding.EncodeToString([]byte(server.Method + ":" + server.Password))
		u.Scheme, u.User = "ss", url.User(creds)
	case "hysteria":
		// Xray pins the certificate hash; sing-box only pins the public key, so
		// the server could not be verified the way the provider asked.
		if o.Stream.TLS.PinnedCert != "" {
			return "", fmt.Errorf("hysteria: пиннинг сертификата не поддерживается")
		}
		if o.Stream.Hysteria.Auth == "" {
			return "", fmt.Errorf("hysteria: нет пароля")
		}
		host, port = o.Settings.Address, o.Settings.Port
		u.Scheme, u.User = "hysteria2", url.User(o.Stream.Hysteria.Auth)
		xrayTLSQuery(o, q)
	default:
		return "", fmt.Errorf("неподдерживаемый протокол: %s", o.Protocol)
	}
	if host == "" || port <= 0 {
		return "", fmt.Errorf("%s: не указан host:port", o.Protocol)
	}
	u.Host = net.JoinHostPort(host, strconv.Itoa(port))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func xrayStreamQuery(o xrayOutbound, q url.Values) {
	s := o.Stream
	if s.Security != "" {
		q.Set("security", s.Security)
	}
	xrayTLSQuery(o, q)
	network := s.Network
	if network == "tcp" || network == "raw" {
		network = ""
	}
	if network != "" {
		q.Set("type", network)
	}
	switch network {
	case "ws":
		host := s.WS.Host
		if host == "" {
			host = s.WS.Headers["Host"]
		}
		setNonEmpty(q, "path", s.WS.Path)
		setNonEmpty(q, "host", host)
	case "grpc":
		setNonEmpty(q, "serviceName", s.GRPC.ServiceName)
	case "httpupgrade":
		setNonEmpty(q, "path", s.HTTPUpgrade.Path)
		setNonEmpty(q, "host", s.HTTPUpgrade.Host)
	case "xhttp", "splithttp":
		setNonEmpty(q, "path", s.XHTTP.Path)
		setNonEmpty(q, "host", s.XHTTP.Host)
		setNonEmpty(q, "mode", s.XHTTP.Mode)
		if len(s.XHTTP.Extra) > 0 {
			q.Set("extra", string(s.XHTTP.Extra))
		}
	}
}

func xrayTLSQuery(o xrayOutbound, q url.Values) {
	if o.Stream.Security == "reality" {
		r := o.Stream.Reality
		setNonEmpty(q, "sni", r.ServerName)
		setNonEmpty(q, "fp", r.Fingerprint)
		setNonEmpty(q, "pbk", r.PublicKey)
		setNonEmpty(q, "sid", r.ShortID)
		return
	}
	t := o.Stream.TLS
	setNonEmpty(q, "sni", t.ServerName)
	setNonEmpty(q, "fp", t.Fingerprint)
	setNonEmpty(q, "alpn", strings.Join(t.ALPN, ","))
	if t.AllowInsecure {
		q.Set("allowInsecure", "1")
	}
}

func setNonEmpty(q url.Values, key, value string) {
	if value != "" {
		q.Set(key, value)
	}
}
