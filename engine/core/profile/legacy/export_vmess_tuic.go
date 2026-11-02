package legacy

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"

	"NekkoVPN/core/profile"
)

// exportVMess writes the v2rayN share format: base64 of a JSON object.
func exportVMess(p profile.Profile) (string, error) {
	c := p.Config
	host := cfgString(c, "server")
	port := cfgInt(c, "server_port")
	uuid := cfgString(c, "uuid")
	if host == "" || port == 0 || uuid == "" {
		return "", fmt.Errorf("vmess: неполный профиль")
	}
	q := url.Values{}
	writeTLSQuery(q, cfgMap(c, "tls"), host)
	writeTransportQuery(q, cfgMap(c, "transport"))
	network := q.Get("type")
	if network == "" {
		network = "tcp"
	}
	path := q.Get("path")
	if network == "grpc" {
		path = q.Get("serviceName")
	}
	sni := q.Get("sni")
	if sni == "" && cfgMap(c, "tls") != nil {
		sni = host
	}
	raw, err := json.Marshal(map[string]interface{}{
		"v": "2", "ps": p.Label, "add": host, "port": port, "id": uuid,
		"aid": cfgInt(c, "alter_id"), "scy": cfgString(c, "security"),
		"net": network, "host": q.Get("host"), "path": path,
		"tls": q.Get("security"), "sni": sni, "alpn": q.Get("alpn"), "fp": q.Get("fp"),
		"allowInsecure": q.Get("allowInsecure"),
	})
	if err != nil {
		return "", err
	}
	return "vmess://" + base64.StdEncoding.EncodeToString(raw), nil
}

func exportTUIC(p profile.Profile) (string, error) {
	c := p.Config
	host := cfgString(c, "server")
	port := cfgInt(c, "server_port")
	uuid := cfgString(c, "uuid")
	if host == "" || port == 0 || uuid == "" {
		return "", fmt.Errorf("tuic: неполный профиль")
	}
	q := url.Values{}
	writeTLSQuery(q, cfgMap(c, "tls"), host)
	if congestion := cfgString(c, "congestion_control"); congestion != "" {
		q.Set("congestion_control", congestion)
	}
	if relay := cfgString(c, "udp_relay_mode"); relay != "" {
		q.Set("udp_relay_mode", relay)
	}
	return buildURI("tuic", uuid, cfgString(c, "password"), host, port, q, p.Label), nil
}
