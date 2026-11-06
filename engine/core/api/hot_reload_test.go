package api

import (
	"context"
	"errors"
	"strings"
	"testing"

	"NekkoVPN/core/engine"
)

// hotEngine applies a reload in place unless told to decline or fail it.
type hotEngine struct {
	syncEngine
	reloads  int
	decline  string
	fail     error
	reloaded engine.Configuration
}

func (f *hotEngine) Reload(context.Context) (engine.ReloadOutcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reloads++
	if f.fail != nil {
		return engine.ReloadOutcome{}, f.fail
	}
	if f.decline != "" {
		return engine.ReloadOutcome{Declined: f.decline}, nil
	}
	f.reloaded = f.configuration
	return engine.ReloadOutcome{Applied: true, Replaced: 1}, nil
}

func initHotCore(t *testing.T) (*Core, *hotEngine) {
	t.Helper()
	t.Setenv("HIKKARI_PRESET_CATALOG_URL", "http://127.0.0.1:1")
	t.Setenv("HIKKARI_PRESET_EVENTS_URL", "http://127.0.0.1:1")
	f := &hotEngine{syncEngine: syncEngine{alternateEngine: alternateEngine{caps: engine.Capabilities{TUN: true, DomainRouting: true, IPRouting: true, AutoSelect: true, ProcessRouting: true, FolderRouting: true}}}}
	c := New()
	if err := c.InitWithEngine(manualRoutingDir(t), func(string, engine.Logger) (engine.Backend, error) { return f, nil }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Shutdown() })
	if _, err := c.AddProfileFromURI("vless://key@a.test:443#A"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddProfileFromURI("vless://key@b.test:443#B"); err != nil {
		t.Fatal(err)
	}
	if err := c.StartVPN(); err != nil {
		t.Fatal(err)
	}
	return c, f
}

// A profile change the core takes in place (a fingerprint the hub turned, say)
// must not stop the core.
func rotateFirstProfile(t *testing.T, c *Core) error {
	t.Helper()
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	before, err := c.profiles.Read()
	if err != nil {
		t.Fatal(err)
	}
	after, err := cloneProfiles(before)
	if err != nil {
		t.Fatal(err)
	}
	after.Profiles[0].Connection.URI = "vless://rotated@a.test:443?fp=firefox#A"
	return c.commitProfiles(context.Background(), before, after)
}

func TestProfileChangeIsAppliedInPlace(t *testing.T) {
	c, f := initHotCore(t)
	starts := f.starts
	if err := rotateFirstProfile(t, c); err != nil {
		t.Fatal(err)
	}
	if f.reloads != 1 || f.starts != starts || !f.running {
		t.Fatalf("reloads=%d starts %d->%d running=%v", f.reloads, starts, f.starts, f.running)
	}
	if !strings.Contains(f.reloaded.Profiles[0].Connection.URI, "rotated") {
		t.Fatalf("the reload ran on %q", f.reloaded.Profiles[0].Connection.URI)
	}
	log := c.logger.Tail()
	for _, want := range []string{"[lifecycle] hot reason=profiles-runtime-change applied=true", "reason=profiles-runtime-change running=true", "ok=true"} {
		if !strings.Contains(log, want) {
			t.Fatalf("missing %q:\n%s", want, log)
		}
	}
	if strings.Contains(log, "reason=profiles-runtime-change/stop") {
		t.Fatalf("the core was stopped:\n%s", log)
	}
}

func TestDeclinedOrFailedReloadRestartsTheCore(t *testing.T) {
	for name, set := range map[string]func(*hotEngine){
		"declined": func(f *hotEngine) { f.decline = "changed outside outbounds: route.rules" },
		"failed":   func(f *hotEngine) { f.fail = errors.New("core did not answer") },
	} {
		t.Run(name, func(t *testing.T) {
			c, f := initHotCore(t)
			set(f)
			starts := f.starts
			if err := rotateFirstProfile(t, c); err != nil {
				t.Fatal(err)
			}
			if f.reloads != 1 || f.starts != starts+1 || !f.running {
				t.Fatalf("reloads=%d starts %d->%d running=%v", f.reloads, starts, f.starts, f.running)
			}
			log := c.logger.Tail()
			if !strings.Contains(log, "[lifecycle] hot reason=profiles-runtime-change applied=false") || !strings.Contains(log, "reason=profiles-runtime-change/stop") {
				t.Fatalf("no fallback restart in the log:\n%s", log)
			}
		})
	}
}

func TestDeletingAProfileDropsItInPlace(t *testing.T) {
	c, f := initHotCore(t)
	starts := f.starts
	if err := c.DeleteProfile(f.configuration.Profiles[1].Name); err != nil {
		t.Fatal(err)
	}
	if f.reloads != 1 || f.starts != starts || !f.running {
		t.Fatalf("reloads=%d starts %d->%d running=%v", f.reloads, starts, f.starts, f.running)
	}
	if len(f.reloaded.Profiles) != 1 {
		t.Fatalf("reloaded %d profiles", len(f.reloaded.Profiles))
	}
}
