package svcproto

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"NekkoVPN/core/autotunnel"
	configfile "NekkoVPN/core/engine/singbox/config"
)

var paths = Paths{
	CacheFile:         `C:\Program Files\hikkari\core\cache.db`,
	AutoTunnelRuleSet: `C:\Program Files\hikkari\core\auto-tunnel.json`,
}

// realConfig is what the GUI engine actually generates, plus the kinds of
// profile blocks it adds: a VLESS/Reality outbound with an xhttp transport
// (whose "path" is an HTTP path, not a file) and an AmneziaWG endpoint. The
// autotunnel rule set is attached the way configfile does it: a local source
// rule set naming the GUI's own file — the one local rule set the contract
// allows, and the one whose path the service rewrites.
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
	route, _ := cfg["route"].(map[string]interface{})
	route["rule_set"] = []interface{}{map[string]interface{}{
		"tag": autotunnel.Tag, "type": "local", "format": "source",
		"path": `C:\Users\someone\AppData\Local\hikkari\auto-tunnel.json`,
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

func ruleSetEntry(t *testing.T, cfg map[string]interface{}) map[string]interface{} {
	t.Helper()
	sets, ok := cfg["route"].(map[string]interface{})["rule_set"].([]interface{})
	if !ok || len(sets) != 1 {
		t.Fatalf("rule_set: %v", cfg["route"])
	}
	m, ok := sets[0].(map[string]interface{})
	if !ok {
		t.Fatalf("rule_set[0]: %v", sets[0])
	}
	return m
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

func TestTheAutoTunnelRuleSetIsRewrittenToTheServiceDirectory(t *testing.T) {
	out, err := sanitize(t, realConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	set := ruleSetEntry(t, out)
	if set["tag"] != autotunnel.Tag || set["type"] != "local" || set["format"] != "source" {
		t.Fatalf("entry changed shape: %v", set)
	}
	if set["path"] != paths.AutoTunnelRuleSet {
		t.Fatalf("the GUI's path survived: %v", set["path"])
	}
	// The path the GUI named is never trusted, whatever it pointed at.
	in := realConfig(t)
	ruleSetEntry(t, in)["path"] = `C:\Windows\System32\config\SAM`
	out, err = sanitize(t, in)
	if err != nil {
		t.Fatal(err)
	}
	if got := ruleSetEntry(t, out)["path"]; got != paths.AutoTunnelRuleSet {
		t.Fatalf("path = %v", got)
	}
	// So is the format: the core reads the list we write, in the shape we write.
	in = realConfig(t)
	ruleSetEntry(t, in)["format"] = "binary"
	out, err = sanitize(t, in)
	if err != nil {
		t.Fatal(err)
	}
	if got := ruleSetEntry(t, out)["format"]; got != "source" {
		t.Fatalf("format = %v", got)
	}
}

func TestOnlyTheAutoTunnelRuleSetIsLocal(t *testing.T) {
	foreign := func(entry map[string]interface{}) map[string]interface{} {
		cfg := realConfig(t)
		cfg["route"].(map[string]interface{})["rule_set"] = []interface{}{entry}
		return cfg
	}

	// Another tag, local: refused.
	mustRefuse(t, foreign(map[string]interface{}{
		"tag": "geosite", "type": "local", "format": "source", "path": `C:\x\geosite.srs`,
	}), "local rule sets are not allowed")

	// The right tag but remote: a URL the core would fetch as SYSTEM.
	mustRefuse(t, foreign(map[string]interface{}{
		"tag": autotunnel.Tag, "type": "remote", "format": "source", "url": "https://example.ru/x.srs",
	}), "remote rule sets are not allowed")

	// A second entry of any kind: the GUI compiles exactly one.
	two := realConfig(t)
	two["route"].(map[string]interface{})["rule_set"] = []interface{}{
		map[string]interface{}{"tag": autotunnel.Tag, "type": "local", "format": "source", "path": `C:\a`},
		map[string]interface{}{"tag": autotunnel.Tag, "type": "local", "format": "source", "path": `C:\b`},
	}
	mustRefuse(t, two, "the GUI compiles one")

	// A non-object entry.
	cfg := realConfig(t)
	cfg["route"].(map[string]interface{})["rule_set"] = []interface{}{"nope"}
	mustRefuse(t, cfg, "not an object")
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
		`C:\Program Files\hikkari\HikkariService.exe`
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

	// The autotunnel list rides the same envelope.
	var buf2 bytes.Buffer
	if err := WriteMessage(&buf2, Request{Op: OpAutoTunnel, Hosts: []string{"a.example"}}); err != nil {
		t.Fatal(err)
	}
	var req2 Request
	if err := ReadMessage(&buf2, &req2); err != nil || req2.Op != OpAutoTunnel {
		t.Fatalf("autotunnel round trip: %v %+v", err, req2)
	}
	if len(req2.Hosts) != 1 || req2.Hosts[0] != "a.example" {
		t.Fatalf("hosts: %v", req2.Hosts)
	}

	huge := bytes.Repeat([]byte("a"), MaxMessage+10)
	if err := ReadMessage(bytes.NewReader(huge), &req); err == nil {
		t.Fatal("oversized message accepted")
	}
}

// The contract end to end: what the GUI's config compiler actually writes
// (configfile.RebuildActive, autotunnel rule set included) passes Sanitize,
// and the rule set path the compiler named is replaced by the service's.
func TestTheCompiledConfigPassesSanitize(t *testing.T) {
	dir := t.TempDir()
	store := configfile.New(filepath.Join(dir, "config.json"))
	specs := []configfile.ProfileSpec{{
		Tag: "p-1", Kind: "outbound",
		Config: map[string]interface{}{"tag": "p-1", "type": "vless", "server": "1.2.3.4", "server_port": 443},
	}}
	if err := store.RebuildActive(specs, "p-1", false, nil); err != nil {
		t.Fatal(err)
	}
	cfg, err := store.Read()
	if err != nil {
		t.Fatal(err)
	}
	svcDir := filepath.Join(dir, "svc")
	out, err := Sanitize(mustJSON(t, cfg), Paths{CacheFile: filepath.Join(svcDir, "cache.db"), AutoTunnelRuleSet: filepath.Join(svcDir, "auto-tunnel.json")})
	if err != nil {
		t.Fatalf("the compiled config was refused: %v", err)
	}
	var back map[string]interface{}
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	sets := back["route"].(map[string]interface{})["rule_set"].([]interface{})
	if len(sets) != 1 {
		t.Fatalf("rule_set: %v", sets)
	}
	set := sets[0].(map[string]interface{})
	if set["path"] != filepath.Join(svcDir, "auto-tunnel.json") {
		t.Fatalf("rule set path = %v", set["path"])
	}
}

func mustJSON(t *testing.T, v interface{}) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
