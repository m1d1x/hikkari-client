package svcproto

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"NekkoVPN/core/configfile"
)

var paths = Paths{CacheFile: `C:\Program Files\Hikkari\core\cache.db`}

// realConfig is what the GUI engine actually generates, plus the kinds of
// profile blocks it adds: a VLESS/Reality outbound with an xhttp transport
// (whose "path" is an HTTP path, not a file) and an AmneziaWG endpoint.
func realConfig(t *testing.T) map[string]interface{} {
	t.Helper()
	cfg := configfile.DefaultConfig()
	cfg["outbounds"] = append(cfg["outbounds"].([]interface{}), map[string]interface{}{
		"tag": "nl-1", "type": "vless", "server": "1.2.3.4", "server_port": 443,
		"uuid": "00000000-0000-0000-0000-000000000000",
		"tls": map[string]interface{}{
			"enabled": true, "server_name": "www.microsoft.com",
			"utls":    map[string]interface{}{"enabled": true, "fingerprint": "chrome"},
			"reality": map[string]interface{}{"enabled": true, "public_key": "pk", "short_id": "ab"},
		},
		"transport": map[string]interface{}{"type": "xhttp", "path": "/artwork/api/uploads"},
	})
	cfg["endpoints"] = []interface{}{map[string]interface{}{
		"tag": "awg-1", "type": "wireguard", "private_key": "k",
		"address": []interface{}{"10.0.0.2/32"},
	}}
	return cfg
}

func sanitize(t *testing.T, cfg map[string]interface{}) (map[string]interface{}, error) {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Sanitize(raw, paths)
	if err != nil {
		return nil, err
	}
	var back map[string]interface{}
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	return back, nil
}

func mustRefuse(t *testing.T, cfg map[string]interface{}, want string) {
	t.Helper()
	_, err := sanitize(t, cfg)
	if err == nil {
		t.Fatalf("accepted a config that should be refused (%s)", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("refused for the wrong reason: %v (want %q)", err, want)
	}
}

func TestTheConfigTheGUIGeneratesIsAccepted(t *testing.T) {
	out, err := sanitize(t, realConfig(t))
	if err != nil {
		t.Fatalf("the GUI's own config was refused: %v", err)
	}
	cache := out["experimental"].(map[string]interface{})["cache_file"].(map[string]interface{})
	if cache["path"] != paths.CacheFile {
		t.Fatalf("cache file not moved to the service's directory: %v", cache["path"])
	}
	// Everything the network depends on survives untouched.
	if len(out["outbounds"].([]interface{})) != 2 || len(out["endpoints"].([]interface{})) != 1 {
		t.Fatalf("outbounds/endpoints changed: %v", out)
	}
}

func TestLogOutputIsTakenAwayFromTheConfig(t *testing.T) {
	cfg := realConfig(t)
	cfg["log"].(map[string]interface{})["output"] = `C:\Windows\System32\evil.dll`
	out, err := sanitize(t, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := out["log"].(map[string]interface{})["output"]; present {
		t.Fatal("log.output survived")
	}
}

func TestACacheFilePointedElsewhereIsPutBack(t *testing.T) {
	cfg := realConfig(t)
	cfg["experimental"].(map[string]interface{})["cache_file"].(map[string]interface{})["path"] =
		`C:\Program Files\Hikkari\HikkariService.exe`
	out, err := sanitize(t, cfg)
	if err != nil {
		t.Fatal(err)
	}
	got := out["experimental"].(map[string]interface{})["cache_file"].(map[string]interface{})["path"]
	if got != paths.CacheFile {
		t.Fatalf("cache path = %v", got)
	}
}

func TestFileReadingKeysAnywhereAreRefused(t *testing.T) {
	cfg := realConfig(t)
	tls := cfg["outbounds"].([]interface{})[1].(map[string]interface{})["tls"].(map[string]interface{})
	tls["certificate_path"] = `C:\Users\other\secret.pem`
	mustRefuse(t, cfg, "outbounds[1].tls.certificate_path")

	cfg = realConfig(t)
	cfg["endpoints"].([]interface{})[0].(map[string]interface{})["private_key_path"] = `C:\x`
	mustRefuse(t, cfg, "private_key_path")

	cfg = realConfig(t)
	cfg["endpoints"] = []interface{}{map[string]interface{}{"type": "tailscale", "state_directory": `C:\`}}
	mustRefuse(t, cfg, "state_directory")
}

func TestTheClashAPIStaysOnLoopbackAndServesNoFiles(t *testing.T) {
	cfg := realConfig(t)
	api := cfg["experimental"].(map[string]interface{})["clash_api"].(map[string]interface{})
	api["external_controller"] = "0.0.0.0:9090"
	mustRefuse(t, cfg, "loopback")

	cfg = realConfig(t)
	api = cfg["experimental"].(map[string]interface{})["clash_api"].(map[string]interface{})
	api["external_ui"] = `C:\`
	mustRefuse(t, cfg, "external_ui")

	cfg = realConfig(t)
	api = cfg["experimental"].(map[string]interface{})["clash_api"].(map[string]interface{})
	api["external_controller"] = "[::1]:9090"
	if _, err := sanitize(t, cfg); err != nil {
		t.Fatalf("IPv6 loopback refused: %v", err)
	}
}

func TestUnknownSectionsAreRefused(t *testing.T) {
	cfg := realConfig(t)
	cfg["services"] = []interface{}{}
	mustRefuse(t, cfg, `"services"`)

	cfg = realConfig(t)
	cfg["experimental"].(map[string]interface{})["v2ray_api"] = map[string]interface{}{"listen": "0.0.0.0:1"}
	mustRefuse(t, cfg, "v2ray_api")
}

func TestLocalRuleSetsAreRefused(t *testing.T) {
	cfg := realConfig(t)
	cfg["route"].(map[string]interface{})["rule_set"] = []interface{}{
		map[string]interface{}{"tag": "x", "type": "local", "format": "binary", "path": `C:\Windows\x`},
	}
	mustRefuse(t, cfg, "local rule sets")
}

func TestGarbageIsRefused(t *testing.T) {
	for _, raw := range []string{"", "null", "[]", "not json", `{"log":`} {
		if _, err := Sanitize([]byte(raw), paths); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}

func TestMessagesRoundTripAndOversizeIsRefused(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteMessage(&buf, Request{Op: OpStart, Config: json.RawMessage(`{"log":{}}`)}); err != nil {
		t.Fatal(err)
	}
	var req Request
	if err := ReadMessage(&buf, &req); err != nil || req.Op != OpStart || string(req.Config) != `{"log":{}}` {
		t.Fatalf("round trip: %v %+v", err, req)
	}

	huge := bytes.Repeat([]byte("a"), MaxMessage+10)
	if err := ReadMessage(bytes.NewReader(huge), &req); err == nil {
		t.Fatal("oversized message accepted")
	}
}
