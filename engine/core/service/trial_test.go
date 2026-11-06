//go:build darwin

package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"NekkoVPN/core/svcproto"
)

// tester-004, 06.11: a rollback from 1.0.51 to 1.0.40 left the 1.0.51 GUI
// running, and every start it sent put 1.0.51's rules in front of 1.0.40's
// core, which refused them field by field. A start from a release other than
// the installed one is refused, with a code the GUI reopens itself on, and
// nothing stored changes.
func TestStartFromAnotherReleaseIsRefused(t *testing.T) {
	versionFile := filepath.Join(t.TempDir(), "VERSION")
	if err := os.WriteFile(versionFile, []byte("1.0.40\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := updateServer(t, Config{VersionFile: versionFile})
	start := func(app, hash string) svcproto.Response {
		return s.dispatch(svcproto.Request{
			Op:         svcproto.OpStart,
			Config:     json.RawMessage(`{"inbounds":[],"log":{"level":"` + hash + `"}}`),
			ConfigHash: hash,
			AppVersion: app,
		})
	}
	if resp := start("1.0.40", "installed"); !resp.OK {
		t.Fatalf("a start from the installed release: %+v", resp)
	}
	stored, err := os.ReadFile(s.configPath(svcproto.EngineSingBox))
	if err != nil {
		t.Fatal(err)
	}
	resp := start("1.0.51", "other")
	if resp.OK || resp.Code != svcproto.CodeAppReplaced {
		t.Fatalf("a start from another release: %+v", resp)
	}
	got, err := os.ReadFile(s.configPath(svcproto.EngineSingBox))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(stored) {
		t.Fatalf("the refused start rewrote the config: %s", got)
	}
	if !coreUp(s) {
		t.Fatal("the refused start took the core down")
	}
	// A GUI that does not say (a development build, a release from before
	// AppVersion) is not refused.
	if resp := start("", "unsaid"); !resp.OK {
		t.Fatalf("a start without a release: %+v", resp)
	}
}

// trialServer is a server whose updater has a version on trial since applied
// and records a rollback in the file it returns.
func trialServer(t *testing.T, applied time.Time, deadline time.Duration) (*Server, string) {
	t.Helper()
	s := updateServer(t, Config{TrialDeadline: deadline})
	rolled := filepath.Join(t.TempDir(), "rolled-back")
	script := `#!/bin/sh
case "$1" in
status) [ -f "` + rolled + `" ] && { echo '{"event":"status","installed":"1.0.0"}'; exit 0; }
        echo '{"event":"status","installed":"1.0.1","pending":{"applied_at":` + strconv.FormatInt(applied.Unix(), 10) + `}}' ;;
recover) echo '{"event":"recovered","result":"on_trial","from":"1.0.0","to":"1.0.1"}' ;;
rollback) : > "` + rolled + `"; echo '{"event":"rolled_back","from":"1.0.0","to":"1.0.1"}' ;;
esac
`
	if err := os.WriteFile(s.cfg.Updater, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return s, rolled
}

func watchTrialFor(t *testing.T, s *Server, within time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	done := make(chan struct{})
	go func() { s.updates.watchTrial(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(within + 5*time.Second):
		t.Fatal("watchTrial did not return")
	}
}

// A GUI of the new version that has reported in owns its trial: the deadline
// is for a version that never came up, not for a slow first launch
// (tester-004: 86 s to the first request, rolled back at 90 s with 0 failed
// starts).
func TestTrialWithItsGUIUpIsNotRolledBackAtTheDeadline(t *testing.T) {
	s, rolled := trialServer(t, time.Now(), 2*time.Second)
	if _, _, _, err := s.updates.recover(); err != nil {
		t.Fatal(err)
	}
	watchTrialFor(t, s, 4*time.Second)
	if _, err := os.Stat(rolled); err == nil {
		t.Fatal("the version on trial was rolled back with its GUI up")
	}
}

func TestTrialWithoutAGUIIsRolledBackAtTheDeadline(t *testing.T) {
	s, rolled := trialServer(t, time.Now(), 2*time.Second)
	watchTrialFor(t, s, 4*time.Second)
	if _, err := os.Stat(rolled); err != nil {
		t.Fatal("no GUI came up and the version on trial stayed")
	}
}

// A rollback with no kept copy (the swap was made by a service from before
// keepConfigForRollback) leaves the newer release's config. The older core is
// not started on it: the core stays wanted for the installed GUI's config.
func TestRollbackOntoANewerConfigLeavesTheCoreForTheApp(t *testing.T) {
	versionFile := filepath.Join(t.TempDir(), "VERSION")
	s := updateServer(t, Config{VersionFile: versionFile})
	startCoreForTest(t, s, "new")
	if err := os.WriteFile(s.configVersionPath(svcproto.EngineSingBox), []byte("1.0.1"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The updater swaps 1.0.0 back in.
	if err := os.WriteFile(versionFile, []byte("1.0.0"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.updates.rollback(); err != nil {
		t.Fatal(err)
	}
	if coreUp(s) {
		t.Fatal("the older core was started on the newer release's config")
	}
	if !s.wanted() {
		t.Fatal("the core is no longer wanted: the GUI would not bring it up")
	}
	// The same at the next service start.
	s.resume()
	if coreUp(s) {
		t.Fatal("resume started the older core on the newer release's config")
	}
}

// An older config is what an update resumes on: the newer core reads it.
func TestRollbackOntoAnOlderConfigBringsTheCoreBack(t *testing.T) {
	versionFile := filepath.Join(t.TempDir(), "VERSION")
	if err := os.WriteFile(versionFile, []byte("1.0.1"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := updateServer(t, Config{VersionFile: versionFile})
	startCoreForTest(t, s, "old")
	if err := os.WriteFile(s.configVersionPath(svcproto.EngineSingBox), []byte("1.0.0"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.updates.rollback(); err != nil {
		t.Fatal(err)
	}
	if !coreUp(s) {
		t.Fatal("the core did not come back on an older config")
	}
}

func TestReleaseNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"1.0.51", "1.0.40", true},
		{"1.0.40", "1.0.51", false},
		{"1.0.10", "1.0.9", true},
		{"2.0.0", "1.9.9", true},
		{"1.0.1", "1.0.1", false},
		{"dev", "1.0.1", false},
		{"1.0.1", "", false},
	} {
		if got := releaseNewer(c.a, c.b); got != c.want {
			t.Errorf("releaseNewer(%q, %q) = %v", c.a, c.b, got)
		}
	}
}
