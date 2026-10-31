package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// initCore returns a Core wired to a fresh temp baseDir. Init starts the
// uptime ping loop; the caller must defer c.Shutdown() to stop it.
func initCore(t *testing.T) *Core {
	t.Helper()
	c := New()
	if err := c.InitWithEngine(t.TempDir(), testEngine); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = c.Shutdown() })
	return c
}

// decode parses a Call() result envelope into ok/data/err for assertions.
func decode(t *testing.T, raw string) Envelope {
	t.Helper()
	var e Envelope
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		t.Fatalf("envelope decode: %v (raw=%s)", err, raw)
	}
	return e
}

func TestCall_UnknownMethod(t *testing.T) {
	c := initCore(t)
	e := decode(t, c.Call("DoesNotExist", ""))
	if e.Ok {
		t.Fatalf("expected ok=false, got %+v", e)
	}
	if !strings.Contains(e.Error, "unknown method") {
		t.Fatalf("want 'unknown method' in error, got %q", e.Error)
	}
}

func TestCall_BadArgs(t *testing.T) {
	c := initCore(t)
	e := decode(t, c.Call("AddRule", "not-json"))
	if e.Ok {
		t.Fatalf("expected ok=false for bad args, got %+v", e)
	}
	if !strings.Contains(e.Error, "bad args JSON") {
		t.Fatalf("want 'bad args JSON' in error, got %q", e.Error)
	}
}

func TestCall_NoArgsTolerated(t *testing.T) {
	c := initCore(t)
	// IsRunning takes no args. Empty/"null"/{} should all work.
	for _, args := range []string{"", "null", "{}"} {
		e := decode(t, c.Call("IsRunning", args))
		if !e.Ok {
			t.Fatalf("IsRunning(%q): want ok, got error %q", args, e.Error)
		}
	}
}

func TestCall_IsRunning_ReturnsBool(t *testing.T) {
	c := initCore(t)
	e := decode(t, c.Call("IsRunning", ""))
	if !e.Ok {
		t.Fatalf("IsRunning: %s", e.Error)
	}
	var b bool
	if err := json.Unmarshal(e.Data, &b); err != nil {
		t.Fatalf("decode bool: %v", err)
	}
	// If a real engine happens to be running on the dev box (port 9090),
	// we just verify the call returned a bool and don't assert false —
	// this test runs on the same machine that hosts the app.
	if vpnClashAPIRunningProbe() {
		t.Logf("clash API up locally; skipping false-assert")
		return
	}
	if b {
		t.Fatalf("IsRunning should be false in test env")
	}
}

// vpnClashAPIRunningProbe re-implements vpn.ClashAPIRunning for the test
// without taking the import (which would pull windows-only code into the
// test binary). Same 600ms /version probe.
func vpnClashAPIRunningProbe() bool {
	cl := &http.Client{Timeout: 600 * time.Millisecond}
	resp, err := cl.Get("http://127.0.0.1:9090/version")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}

func TestCall_UptimeHistory_ReturnsObject(t *testing.T) {
	c := initCore(t)
	e := decode(t, c.Call("UptimeHistory", ""))
	if !e.Ok {
		t.Fatalf("UptimeHistory: %s", e.Error)
	}
	var m map[string][]PingSample
	if err := json.Unmarshal(e.Data, &m); err != nil {
		t.Fatalf("decode map: %v", err)
	}
	// Fresh temp dir → empty map (or nil — both fine).
	if len(m) != 0 {
		t.Fatalf("expected empty history, got %+v", m)
	}
}

func TestCall_AddRule_RoundTrip(t *testing.T) {
	c := initCore(t)
	// AddRule + Routing round-trip — seed a minimal config so applyRouting
	// has something to write back into.
	seed := []byte(`{"route":{"rules":[]}}`)
	if err := writeFile(filepath.Join(c.BaseDir(), "config.json"), seed); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	args := `{"mode":"bypass","category":"domains","value":"example.com"}`
	e := decode(t, c.Call("AddRule", args))
	if !e.Ok {
		t.Fatalf("AddRule: %s", e.Error)
	}

	e = decode(t, c.Call("Routing", ""))
	if !e.Ok {
		t.Fatalf("Routing: %s", e.Error)
	}
	var rs RoutingState
	if err := json.Unmarshal(e.Data, &rs); err != nil {
		t.Fatalf("decode routing: %v", err)
	}
	if len(rs.Bypass.Domains) != 1 || rs.Bypass.Domains[0] != "example.com" {
		t.Fatalf("expected bypass.domains=[example.com], got %+v", rs.Bypass.Domains)
	}
}

func TestCall_SwitchProfile_UpdatesConfigAndActive(t *testing.T) {
	// SwitchProfile now tries to talk to a running Clash API if one is up.
	// On a dev box where the real engine is running, the call will 404
	// against our synthetic test profiles — skip the test there.
	if vpnClashAPIRunningProbe() {
		t.Skip("real engine running on 127.0.0.1:9090; SwitchProfile would 404")
	}
	c := initCore(t)
	base := c.BaseDir()
	seedConfig := []byte(`{"endpoints":[{"tag":"awg-ep","type":"wireguard","peers":[{"address":"old.example","port":1111}]}],"route":{"rules":[]},"outbounds":[{"tag":"direct","type":"direct"}]}`)
	seedProfiles := []byte(`{"active":"main","profiles":[{"name":"main","label":"Main","endpoint":{"tag":"awg-ep","type":"wireguard","peers":[{"address":"old.example","port":1111}]}},{"name":"next","label":"Next","endpoint":{"tag":"custom-tag","type":"awg","peers":[{"address":"new.example","port":2222}]}}]}`)
	if err := writeFile(filepath.Join(base, "config.json"), seedConfig); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	if err := writeFile(filepath.Join(base, "profiles.json"), seedProfiles); err != nil {
		t.Fatalf("seed profiles: %v", err)
	}

	e := decode(t, c.Call("SwitchProfile", `{"name":"next"}`))
	if !e.Ok {
		t.Fatalf("SwitchProfile: %s", e.Error)
	}

	profiles, err := c.profiles.Read()
	if err != nil {
		t.Fatalf("read profiles: %v", err)
	}
	if profiles.Active != "next" {
		t.Fatalf("expected active next, got %q", profiles.Active)
	}

	cfg, err := readBuiltConfig(c)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	// Both profiles should be loaded as endpoints (Kind defaults to
	// endpoint for legacy seeds without "kind").
	endpoints, _ := cfg["endpoints"].([]interface{})
	if len(endpoints) != 2 {
		t.Fatalf("expected two endpoints, got %d: %+v", len(endpoints), endpoints)
	}

	// Selector outbound should default to the "next" profile's tag.
	obs, _ := cfg["outbounds"].([]interface{})
	var selector map[string]interface{}
	for _, ob := range obs {
		m, _ := ob.(map[string]interface{})
		if t, _ := m["tag"].(string); t == "proxy" {
			selector = m
			break
		}
	}
	if selector == nil {
		t.Fatalf("selector outbound missing: %+v", obs)
	}
	if d, _ := selector["default"].(string); d != "p-next" {
		t.Errorf("selector.default = %q, want p-next", d)
	}

	// route.final must point at the selector.
	if v, _ := cfg["route"].(map[string]interface{})["final"].(string); v != "proxy" {
		t.Errorf("route.final = %q, want proxy", v)
	}
}

func TestInit_TwiceWithDifferentBaseDirFails(t *testing.T) {
	c := New()
	if err := c.InitWithEngine(t.TempDir(), testEngine); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	defer c.Shutdown()
	if err := c.InitWithEngine(t.TempDir(), testEngine); err == nil {
		t.Fatal("expected error when re-initing with a different baseDir")
	}
}

// Init must be idempotent for the same baseDir — Flutter hot restart reboots
// the Dart isolate but the host DLL stays loaded, and the smoke-test app
// calls Init in every initState().
func TestInit_SameBaseDirIsNoop(t *testing.T) {
	c := New()
	dir := t.TempDir()
	if err := c.InitWithEngine(dir, testEngine); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	defer c.Shutdown()
	if err := c.InitWithEngine(dir, testEngine); err != nil {
		t.Fatalf("re-Init with same dir should be no-op, got %v", err)
	}
}

func TestInit_RequiresBaseDir(t *testing.T) {
	c := New()
	if err := c.InitWithEngine("", testEngine); err == nil {
		t.Fatal("expected error on empty baseDir")
	}
}

func TestShutdown_Idempotent(t *testing.T) {
	c := initCore(t)
	if err := c.Shutdown(); err != nil {
		t.Fatalf("first shutdown: %v", err)
	}
	if err := c.Shutdown(); err != nil {
		t.Fatalf("second shutdown: %v", err)
	}
}

func TestEnvelope_AlwaysValidJSON(t *testing.T) {
	c := initCore(t)
	// Force an error path and verify the result still parses as JSON.
	raw := c.Call("NopeNotAMethod", "")
	var any map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &any); err != nil {
		t.Fatalf("envelope must be valid JSON, got %q: %v", raw, err)
	}
}

func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0644)
}

// Verify the full import path persists every server separately, including
// repeated display labels, and makes all of them selectable in sing-box.
func TestCall_ImportSubscriptionsKeepsEveryServer(t *testing.T) {
	if vpnClashAPIRunningProbe() {
		t.Skip("real VPN is running")
	}
	c := initCore(t)
	body := "vless://id@fi.example:443#Server\nvless://id@de.example:443#Server"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString([]byte(body))))
	}))
	defer server.Close()
	args, _ := json.Marshal(map[string]string{"uri": "[one](" + server.URL + "/one)\n[second](" + server.URL + "/two)"})
	result := decode(t, c.Call("AddProfileFromURI", string(args)))
	if !result.Ok {
		t.Fatal(result.Error)
	}
	profiles, err := c.profiles.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles.Profiles) != 4 {
		t.Fatalf("got %d profiles, want 4", len(profiles.Profiles))
	}
	names := map[string]bool{}
	for _, p := range profiles.Profiles {
		if names[p.Name] {
			t.Fatalf("duplicate profile name %q", p.Name)
		}
		names[p.Name] = true
	}
	cfg, err := readBuiltConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	var servers, selectable int
	for _, raw := range cfg["outbounds"].([]interface{}) {
		ob := raw.(map[string]interface{})
		if ob["type"] == "vless" {
			servers++
		}
		if ob["tag"] == "proxy" {
			selectable = len(ob["outbounds"].([]interface{}))
		}
	}
	if servers != 4 || selectable != 4 {
		t.Fatalf("outbounds=%d selector=%d, want 4 each", servers, selectable)
	}
}

func readBuiltConfig(c *Core) (map[string]interface{}, error) {
	data, err := os.ReadFile(filepath.Join(c.BaseDir(), "config.json"))
	if err != nil {
		return nil, err
	}
	var cfg map[string]interface{}
	err = json.Unmarshal(data, &cfg)
	return cfg, err
}
