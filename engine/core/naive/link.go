// Package naive defines the share-link contract used by the client and providers.
//
// It also carries Hikkari v1 (hikkari+v1://): the same Naive link and
// credential, plus a datagram channel for UDP on the same host and port. The
// core's hikkari outbound sends TCP over Naive and UDP over the datagram
// channel, falling back to UDP-over-TCP inside Naive. dup=1 (v1.02): the relay
// serves duplicated UDP, and a call rides both channels at once.
package naive

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

type Link struct {
	Host, Username, Password, ServerName, Label string
	Port                                        int
	QUIC                                        bool
	// Hikkari is a hikkari+v1:// link: Naive for TCP, QUIC datagrams for UDP.
	Hikkari bool
	// Dup is dup=1 on a hikkari+v1:// link: v1.02 duplicated UDP.
	Dup bool
}

func Parse(u *url.URL) (Link, error) {
	var l Link
	scheme := strings.ToLower(u.Scheme)
	if scheme != "naive+https" && scheme != "naive+quic" && scheme != "hikkari+v1" {
		return l, fmt.Errorf("naive: неподдерживаемая схема")
	}
	l.Host, l.Port, l.QUIC, l.Hikkari, l.Label = u.Hostname(), 443, scheme == "naive+quic", scheme == "hikkari+v1", u.Fragment
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return l, fmt.Errorf("naive: неверный порт")
		}
		l.Port = port
	}
	if l.Host == "" || strings.HasSuffix(u.Host, ":") {
		return l, fmt.Errorf("naive: не указан сервер или порт")
	}
	if u.User == nil {
		return l, fmt.Errorf("naive: нужны имя пользователя и пароль")
	}
	l.Username = u.User.Username()
	l.Password, _ = u.User.Password()
	if l.Username == "" || l.Password == "" || strings.Contains(l.Username, ":") {
		return l, fmt.Errorf("naive: нужны имя пользователя и пароль")
	}
	if u.Opaque != "" || (u.Path != "" && u.Path != "/") {
		return l, fmt.Errorf("naive: путь в ссылке не поддерживается")
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return l, fmt.Errorf("naive: неверные параметры ссылки")
	}
	for key, values := range q {
		known := key == "sni" || key == "hikkari-node" || (key == "dup" && l.Hikkari)
		if !known || len(values) != 1 {
			return l, fmt.Errorf("naive: неподдерживаемые параметры ссылки")
		}
	}
	switch q.Get("dup") {
	case "":
	case "1":
		l.Dup = true
	default:
		return l, fmt.Errorf("naive: неверный параметр dup")
	}
	l.ServerName = q.Get("sni")
	if l.ServerName == "" {
		l.ServerName = l.Host
	}
	if l.Label == "" {
		l.Label = l.Host
	}
	return l, nil
}

func (l Link) Outbound() map[string]interface{} {
	if l.Hikkari {
		o := map[string]interface{}{
			"type": "hikkari", "server": l.Host, "server_port": l.Port,
			"username": l.Username, "password": l.Password,
			"tls": map[string]interface{}{"enabled": true, "server_name": l.ServerName},
		}
		if l.Dup {
			o["udp"] = map[string]interface{}{"dup": true}
		}
		return o
	}
	return map[string]interface{}{
		"type": "naive", "server": l.Host, "server_port": l.Port,
		"username": l.Username, "password": l.Password, "quic": l.QUIC,
		"tls": map[string]interface{}{"enabled": true, "server_name": l.ServerName},
	}
}

func (l Link) URI() string {
	scheme := "naive+https"
	if l.QUIC {
		scheme = "naive+quic"
	}
	if l.Hikkari {
		scheme = "hikkari+v1"
	}
	u := url.URL{Scheme: scheme, Host: net.JoinHostPort(l.Host, strconv.Itoa(l.Port)),
		User: url.UserPassword(l.Username, l.Password), Fragment: l.Label}
	q := url.Values{}
	if l.ServerName != "" && l.ServerName != l.Host {
		q.Set("sni", l.ServerName)
	}
	if l.Hikkari && l.Dup {
		q.Set("dup", "1")
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// FromOutbound refuses settings a share link cannot preserve. In particular,
// importing a provider document must never silently discard TLS verification.
func FromOutbound(o map[string]interface{}, label string) (Link, error) {
	var l Link
	if o["type"] != "naive" && o["type"] != "hikkari" {
		return l, fmt.Errorf("naive: неверный тип профиля")
	}
	l.Hikkari = o["type"] == "hikkari"
	for key := range o {
		switch key {
		case "type", "tag", "server", "server_port", "username", "password", "quic", "tls":
		case "udp":
			// Only v1.02's dup fits a link.
			udp, ok := o["udp"].(map[string]interface{})
			dup, isBool := udp["dup"].(bool)
			if !l.Hikkari || !ok || len(udp) != 1 || !isBool {
				return l, fmt.Errorf("naive: настройки профиля не помещаются в ссылку")
			}
			l.Dup = dup
		default:
			return l, fmt.Errorf("naive: настройки профиля не помещаются в ссылку")
		}
	}
	tls, _ := o["tls"].(map[string]interface{})
	if enabled, _ := tls["enabled"].(bool); !enabled {
		return l, fmt.Errorf("naive: требуется TLS")
	}
	for key := range tls {
		if key != "enabled" && key != "server_name" {
			return l, fmt.Errorf("naive: настройки TLS не помещаются в ссылку")
		}
	}
	l.Host, _ = o["server"].(string)
	l.Username, _ = o["username"].(string)
	l.Password, _ = o["password"].(string)
	l.ServerName, _ = tls["server_name"].(string)
	l.Label = label
	if value, ok := o["quic"]; ok && !l.Hikkari {
		var valid bool
		l.QUIC, valid = value.(bool)
		if !valid {
			return l, fmt.Errorf("naive: неверное поле quic")
		}
	}
	port, err := strconv.Atoi(fmt.Sprint(o["server_port"]))
	if err != nil || port < 1 || port > 65535 || l.Host == "" || l.Username == "" || l.Password == "" || strings.Contains(l.Username, ":") {
		return l, fmt.Errorf("naive: неполный профиль или неверный порт")
	}
	l.Port = port
	return l, nil
}
