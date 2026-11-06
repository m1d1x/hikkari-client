//go:build darwin

package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"NekkoVPN/core/svcproto"
)

// updateServer is a server with a core that runs until killed and an updater that
// answers what the service asks: on success every command reports its event.
func updateServer(t *testing.T, cfg Config) *Server {
	t.Helper()
	dir := t.TempDir()
	core := filepath.Join(dir, "core")
	if err := os.WriteFile(core, []byte(fakeCoreScript), 0o755); err != nil {
		t.Fatal(err)
	}
	updater := filepath.Join(dir, "updater")
	script := `#!/bin/sh
case "$1" in
status) echo '{"event":"status","installed":"1.0.0"}' ;;
apply) [ -f "` + dir + `/apply-fails" ] && { echo '{"event":"error","code":"x","message":"swap failed"}'; exit 1; }
       echo '{"event":"applied","from":"1.0.0","to":"1.0.1"}' ;;
rollback) echo '{"event":"rolled_back","from":"1.0.1","to":"1.0.0"}' ;;
recover) echo '{"event":"recovered","result":"rolled_back","from":"1.0.1","to":"1.0.0"}' ;;
esac
`
	if err := os.WriteFile(updater, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg.CoreDir, cfg.CoreBin, cfg.CoreWorkDir, cfg.Updater, cfg.CoreReady = dir, core, dir, updater, fakeCoreUp
	s, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.stopCores() })
	return s
}

func startCoreForTest(t *testing.T, s *Server, hash string) {
	t.Helper()
	req := svcproto.Request{Op: svcproto.OpStart, Config: json.RawMessage(`{"inbounds":[],"log":{"level":"` + hash + `"}}`), ConfigHash: hash}
	if resp := s.dispatch(req); !resp.OK {
		t.Fatalf("start: %+v", resp)
	}
}

func applyForTest(t *testing.T, s *Server, reconnect bool) error {
	t.Helper()
	s.updates.set(func(st *svcproto.UpdateState) { st.State = svcproto.UpdateReady })
	_, _, err := s.updates.apply(reconnect)
	return err
}

func coreUp(s *Server) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, pid := s.running()
	return pid != 0
}

// The heart of it: an update takes the core down once. It stays down for the new
// release's config, then runs on that.
func TestUpdateBringsTheCoreUpOnceOnTheNewConfig(t *testing.T) {
	s := updateServer(t, Config{})
	startCoreForTest(t, s, "old")
	if err := applyForTest(t, s, true); err != nil {
		t.Fatal(err)
	}
	if coreUp(s) {
		t.Fatal("the core came back on the previous release's config")
	}
	if resp := s.dispatch(svcproto.Request{Op: svcproto.OpStatus}); !resp.Wanted || resp.Running {
		t.Fatalf("status while waiting: %+v", resp)
	}
	startCoreForTest(t, s, "new")
	if !coreUp(s) || s.awaitingConfig() {
		t.Fatal("the new config did not end the wait")
	}
	if resp := s.dispatch(svcproto.Request{Op: svcproto.OpStatus}); resp.ConfigHash != "new" {
		t.Fatalf("hash %q", resp.ConfigHash)
	}
	s.mu.Lock()
	s.startOnStoredConfigIfAwaitExpired(time.Now().Add(time.Hour))
	s.mu.Unlock()
	if !coreUp(s) {
		t.Fatal("the core went away")
	}
}

// With no GUI the tunnel comes back on what the service holds, at the deadline and
// not before.
func TestWithoutAGUITheCoreComesBackAtTheDeadline(t *testing.T) {
	s := updateServer(t, Config{})
	startCoreForTest(t, s, "old")
	if err := applyForTest(t, s, true); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.startOnStoredConfigIfAwaitExpired(time.Now().Add(awaitConfigFor / 2))
	s.mu.Unlock()
	if coreUp(s) {
		t.Fatal("came back before the deadline")
	}
	s.mu.Lock()
	s.startOnStoredConfigIfAwaitExpired(time.Now().Add(awaitConfigFor + time.Second))
	s.mu.Unlock()
	if !coreUp(s) {
		t.Fatal("did not come back at the deadline")
	}
	if s.awaitingConfig() {
		t.Fatal("the wait outlived the deadline")
	}
	if resp := s.dispatch(svcproto.Request{Op: svcproto.OpStatus}); resp.ConfigHash != "old" {
		t.Fatalf("hash %q: the GUI must see the old config and reconnect", resp.ConfigHash)
	}
}

// The wait survives the service being replaced (macOS), and a resume honours it. A
// reboot past the deadline resumes as usual.
func TestResumeHonoursTheWait(t *testing.T) {
	s := updateServer(t, Config{RestartAfterApply: true})
	startCoreForTest(t, s, "old")
	if err := applyForTest(t, s, true); err != nil {
		t.Fatal(err)
	}
	next, err := NewServer(s.cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = next.stopCores() })
	next.resume()
	if coreUp(next) {
		t.Fatal("the new service resumed the old config")
	}
	if err := writeAtomic(next.awaitMarker(), []byte("1")); err != nil {
		t.Fatal(err)
	}
	next.resume()
	if !coreUp(next) || next.awaitingConfig() {
		t.Fatal("an expired wait did not resume")
	}
}

// A user who disconnects while the core waits gets no tunnel at the deadline.
func TestDisconnectEndsTheWait(t *testing.T) {
	s := updateServer(t, Config{})
	startCoreForTest(t, s, "old")
	if err := applyForTest(t, s, true); err != nil {
		t.Fatal(err)
	}
	if resp := s.dispatch(svcproto.Request{Op: svcproto.OpStop}); !resp.OK {
		t.Fatal(resp.Error)
	}
	if s.awaitingConfig() || s.wanted() {
		t.Fatal("the wish outlived a disconnect")
	}
	s.mu.Lock()
	s.startOnStoredConfigIfAwaitExpired(time.Now().Add(time.Hour))
	s.mu.Unlock()
	if coreUp(s) {
		t.Fatal("the core came up after a disconnect")
	}
}

// A swap that failed moved nothing: the tunnel comes straight back, also where the
// service would have exited (it does not, since handle sees the error).
func TestFailedApplyBringsTheCoreBack(t *testing.T) {
	for name, cfg := range map[string]Config{"stays": {}, "replaced": {RestartAfterApply: true}} {
		t.Run(name, func(t *testing.T) {
			s := updateServer(t, cfg)
			startCoreForTest(t, s, "old")
			if err := os.WriteFile(filepath.Join(s.cfg.CoreDir, "apply-fails"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := applyForTest(t, s, true); err == nil {
				t.Fatal("no error")
			}
			if !coreUp(s) || s.awaitingConfig() {
				t.Fatal("a failed swap left the core down")
			}
		})
	}
}

// Rollback brings the core up itself, on the config the previous release wrote: the
// new GUI may already have sent one the previous core cannot read.
func TestRollbackRestoresThePreviousConfig(t *testing.T) {
	roll := map[string]func(s *Server) error{
		"rollback": func(s *Server) error { _, _, err := s.updates.rollback(); return err },
		"recover": func(s *Server) error {
			script := []byte("#!/bin/sh\ncase \"$1\" in\nstatus) echo '{\"event\":\"status\",\"recover_moves\":true}' ;;\nrecover) echo '{\"event\":\"recovered\",\"result\":\"rolled_back\",\"from\":\"1.0.1\",\"to\":\"1.0.0\"}' ;;\nesac\n")
			if err := os.WriteFile(s.cfg.Updater, script, 0o755); err != nil {
				return err
			}
			_, _, _, err := s.updates.recover()
			return err
		},
	}
	for name, do := range roll {
		for _, sent := range []bool{false, true} {
			t.Run(name, func(t *testing.T) {
				s := updateServer(t, Config{})
				startCoreForTest(t, s, "old")
				oldConfig, err := os.ReadFile(s.configPath(svcproto.EngineSingBox))
				if err != nil {
					t.Fatal(err)
				}
				if err := applyForTest(t, s, true); err != nil {
					t.Fatal(err)
				}
				if sent {
					startCoreForTest(t, s, "new")
				}
				if err := do(s); err != nil {
					t.Fatal(err)
				}
				if !coreUp(s) || s.awaitingConfig() {
					t.Fatal("the rollback left the core down")
				}
				got, err := os.ReadFile(s.configPath(svcproto.EngineSingBox))
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != string(oldConfig) {
					t.Fatalf("the core runs %s", got)
				}
				if resp := s.dispatch(svcproto.Request{Op: svcproto.OpStatus}); resp.ConfigHash != "old" {
					t.Fatalf("hash %q", resp.ConfigHash)
				}
			})
		}
	}
}

// A confirmed version keeps its config: nothing to go back to, and nothing that a
// later rollback of another version could confuse.
func TestConfirmForgetsThePreviousConfig(t *testing.T) {
	s := updateServer(t, Config{})
	startCoreForTest(t, s, "old")
	if err := applyForTest(t, s, true); err != nil {
		t.Fatal(err)
	}
	if err := s.updates.confirm(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.configPath(svcproto.EngineSingBox) + rollbackSuffix); !os.IsNotExist(err) {
		t.Fatalf("the kept config is still there: %v", err)
	}
}

// An update with the tunnel off leaves it off.
func TestUpdateWithTheTunnelOffStaysOff(t *testing.T) {
	s := updateServer(t, Config{})
	startCoreForTest(t, s, "old")
	if resp := s.dispatch(svcproto.Request{Op: svcproto.OpStop}); !resp.OK {
		t.Fatal(resp.Error)
	}
	if err := applyForTest(t, s, false); err != nil {
		t.Fatal(err)
	}
	if coreUp(s) || s.wanted() || s.awaitingConfig() {
		t.Fatal("the update asked for a core nobody wanted")
	}
}
