//go:build darwin

package vpn

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// End-to-end check of the sentinel protocol against a stand-in for sing-box:
// creating the file must start the core, removing it must stop it. The second
// half is the regression — launchd's own KeepAlive/PathState starts a job on
// the file appearing but never stops it when it goes away, which is how
// "disconnect" ended up doing nothing at all.
//
// Runs the supervisor as the current user (the fake core needs no privileges),
// so it exercises the generated script itself, not a mock of it.
func TestSupervisorFollowsSentinel(t *testing.T) {
	dir := t.TempDir()
	alive := filepath.Join(dir, "core-alive")

	fakeCore := filepath.Join(dir, "fake-sing-box")
	body := "#!/bin/sh\ntrap 'rm -f " + alive + "; exit 0' TERM INT\ntouch " + alive +
		"\nwhile :; do sleep 0.2; done\n"
	if err := os.WriteFile(fakeCore, []byte(body), 0755); err != nil {
		t.Fatal(err)
	}

	m := NewProcessManager(fakeCore, filepath.Join(dir, "config.json"), dir, testLog{})
	script := filepath.Join(dir, "supervisor.sh")
	if err := os.WriteFile(script, []byte(m.supervisor(fakeCore)), 0700); err != nil {
		t.Fatal(err)
	}

	sup := exec.Command("/bin/sh", script)
	if err := sup.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = sup.Process.Kill()
		_, _ = sup.Process.Wait()
	}()

	if err := os.WriteFile(m.runFile(), []byte("up\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if !waitFor(func() bool { return exists(alive) }, 10*time.Second) {
		t.Fatal("core was not started after the sentinel appeared")
	}

	if err := os.Remove(m.runFile()); err != nil {
		t.Fatal(err)
	}
	if !waitFor(func() bool { return !exists(alive) }, 15*time.Second) {
		t.Fatal("core was not stopped after the sentinel was removed")
	}

	// And back up again: a reconnect must not need the daemon reinstalled.
	if err := os.WriteFile(m.runFile(), []byte("up\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if !waitFor(func() bool { return exists(alive) }, 10*time.Second) {
		t.Fatal("core did not come back on reconnect")
	}
}

// A core that dies on its own — crash, OOM, upstream bug — must be brought
// back while the user still wants the tunnel up.
func TestSupervisorRestartsDeadCore(t *testing.T) {
	dir := t.TempDir()
	alive := filepath.Join(dir, "core-alive")
	generation := filepath.Join(dir, "generation")

	fakeCore := filepath.Join(dir, "fake-sing-box")
	body := "#!/bin/sh\ntrap 'rm -f " + alive + "; exit 0' TERM INT\n" +
		"echo x >> " + generation + "\ntouch " + alive +
		"\nwhile :; do sleep 0.2; done\n"
	if err := os.WriteFile(fakeCore, []byte(body), 0755); err != nil {
		t.Fatal(err)
	}

	m := NewProcessManager(fakeCore, filepath.Join(dir, "config.json"), dir, testLog{})
	script := filepath.Join(dir, "supervisor.sh")
	if err := os.WriteFile(script, []byte(m.supervisor(fakeCore)), 0700); err != nil {
		t.Fatal(err)
	}
	sup := exec.Command("/bin/sh", script)
	if err := sup.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = sup.Process.Kill()
		_, _ = sup.Process.Wait()
	}()

	if err := os.WriteFile(m.runFile(), []byte("up\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if !waitFor(func() bool { return exists(alive) }, 10*time.Second) {
		t.Fatal("core was not started")
	}

	// Kill it the way a crash would, behind the supervisor's back.
	_ = exec.Command("pkill", "-f", fakeCore).Run()
	if !waitFor(func() bool { return countLines(generation) >= 2 }, 15*time.Second) {
		t.Fatal("supervisor did not restart the core after it died")
	}
}

// The core binary is installed root-owned, outside the app bundle, and a
// password prompt is the cost of reinstalling it — so "is the installed copy
// still the one we ship" has to be exact, and has to ignore the fresh mtime
// every app build stamps on the bundled copy.
func TestSameFileIgnoresMtime(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	if err := os.WriteFile(src, []byte("core v1"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("core v1"), 0755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(dst, old, old); err != nil {
		t.Fatal(err)
	}
	if !sameFile(src, dst) {
		t.Error("same bytes with different mtimes should count as installed")
	}

	// Same length, different content — the case size alone cannot catch.
	if err := os.WriteFile(dst, []byte("core v2"), 0755); err != nil {
		t.Fatal(err)
	}
	if sameFile(src, dst) {
		t.Error("different bytes of equal length should count as stale")
	}

	if sameFile(src, filepath.Join(dir, "missing")) {
		t.Error("a missing destination is never up to date")
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func countLines(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n := 0
	for _, c := range b {
		if c == '\n' {
			n++
		}
	}
	return n
}

func waitFor(cond func() bool, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}
