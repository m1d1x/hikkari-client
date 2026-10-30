package configfile

import (
	"errors"
	"net"
	"path/filepath"
	"testing"
)

func TestPinServerIPs(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "config.json"))
	cfg := DefaultConfig()
	cfg["outbounds"] = []interface{}{
		map[string]interface{}{"tag": "direct", "type": "direct"},
		map[string]interface{}{
			"tag": "p-xhttp", "type": "vless", "server": "web.example.ru", "server_port": 443,
			"tls":       map[string]interface{}{"enabled": true},
			"transport": map[string]interface{}{"type": "xhttp", "path": "/x"},
		},
		map[string]interface{}{
			"tag": "p-ws", "type": "vless", "server": "ws.example.ru", "server_port": 443,
			"tls":       map[string]interface{}{"enabled": true, "server_name": "sni.example.ru"},
			"transport": map[string]interface{}{"type": "ws"},
		},
		map[string]interface{}{"tag": "p-ip", "type": "vless", "server": "1.2.3.4", "server_port": 443},
		map[string]interface{}{"tag": "p-fake", "type": "vless", "server": "fake.example.ru", "server_port": 443},
		map[string]interface{}{"tag": "p-fail", "type": "vless", "server": "fail.example.ru", "server_port": 443},
	}
	if err := s.Write(cfg); err != nil {
		t.Fatal(err)
	}
	resolve := func(host string) ([]net.IP, error) {
		switch host {
		case "web.example.ru":
			return []net.IP{net.ParseIP("2001:db8::1"), net.ParseIP("5.6.7.8")}, nil
		case "ws.example.ru":
			return []net.IP{net.ParseIP("9.9.9.9")}, nil
		case "fake.example.ru":
			return []net.IP{net.ParseIP("198.18.0.84")}, nil
		}
		return nil, errors.New("nxdomain")
	}
	res, err := s.PinServerIPs(resolve)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 4 {
		t.Fatalf("want 4 results (ip literal skipped), got %d: %+v", len(res), res)
	}
	got, _ := s.Read()
	obs := got["outbounds"].([]interface{})
	byTag := map[string]map[string]interface{}{}
	for _, o := range obs {
		m := o.(map[string]interface{})
		byTag[m["tag"].(string)] = m
	}
	x := byTag["p-xhttp"]
	if x["server"] != "5.6.7.8" {
		t.Errorf("xhttp server = %v, want 5.6.7.8", x["server"])
	}
	if x["tls"].(map[string]interface{})["server_name"] != "web.example.ru" {
		t.Errorf("xhttp SNI not kept: %v", x["tls"])
	}
	if x["transport"].(map[string]interface{})["host"] != "web.example.ru" {
		t.Errorf("xhttp host not kept: %v", x["transport"])
	}
	w := byTag["p-ws"]
	if w["server"] != "9.9.9.9" || w["tls"].(map[string]interface{})["server_name"] != "sni.example.ru" {
		t.Errorf("ws: %v", w)
	}
	if w["transport"].(map[string]interface{})["headers"].(map[string]interface{})["Host"] != "ws.example.ru" {
		t.Errorf("ws Host header not set: %v", w["transport"])
	}
	if byTag["p-fake"]["server"] != "fake.example.ru" {
		t.Errorf("fake-ip answer must leave hostname, got %v", byTag["p-fake"]["server"])
	}
	if byTag["p-fail"]["server"] != "fail.example.ru" {
		t.Errorf("failed lookup must leave hostname, got %v", byTag["p-fail"]["server"])
	}
}
