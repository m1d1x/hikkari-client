package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// initCore returns a Core wired to a fresh temp baseDir. Init starts the
// uptime ping loop; the caller must defer c.Shutdown() to stop it.
func initCore(t *testing.T) *Core {
	t.Helper()
	c := New()
	if err := c.Init(t.TempDir()); err != nil {
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
	// In a clean test env nothing listens on 127.0.0.1:9090.
	var b bool
	if err := json.Unmarshal(e.Data, &b); err != nil {
		t.Fatalf("decode bool: %v", err)
	}
	if b {
		t.Fatalf("IsRunning should be false in test env")
	}
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
	// Whitelist on an empty config — no config.json, so this should error.
	// We need to seed a minimal config first.
	seed := []byte(`{"route":{"rules":[]}}`)
	if err := writeFile(filepath.Join(c.BaseDir(), "config.json"), seed); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	args := `{"category":"domains","value":"example.com"}`
	e := decode(t, c.Call("AddRule", args))
	if !e.Ok {
		t.Fatalf("AddRule: %s", e.Error)
	}

	e = decode(t, c.Call("Whitelist", ""))
	if !e.Ok {
		t.Fatalf("Whitelist: %s", e.Error)
	}
	var wl Whitelist
	if err := json.Unmarshal(e.Data, &wl); err != nil {
		t.Fatalf("decode whitelist: %v", err)
	}
	if len(wl.Domains) != 1 || wl.Domains[0] != "example.com" {
		t.Fatalf("expected [example.com], got %+v", wl.Domains)
	}
}

func TestInit_TwiceFails(t *testing.T) {
	c := New()
	if err := c.Init(t.TempDir()); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	defer c.Shutdown()
	if err := c.Init(t.TempDir()); err == nil {
		t.Fatal("expected error on double Init")
	}
}

func TestInit_RequiresBaseDir(t *testing.T) {
	c := New()
	if err := c.Init(""); err == nil {
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
