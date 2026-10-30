//go:build darwin

package vpn

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type testLog struct{}

func (testLog) Logf(string, ...interface{})     {}
func (testLog) PipeLines(string, io.ReadCloser) {}

func testManager(t *testing.T) (*ProcessManager, string) {
	t.Helper()
	dir := t.TempDir()
	return NewProcessManager(
		filepath.Join(dir, "sing-box"),
		filepath.Join(dir, "config.json"),
		dir,
		testLog{},
	), dir
}

// The daemon definition is compared byte-for-byte against the installed copy
// to decide whether a password prompt is needed, so it has to be both stable
// and valid plist XML — a malformed one gets the job rejected by launchd with
// nothing but a spent password prompt to show for it.
func TestPlistIsValidAndStable(t *testing.T) {
	m, dir := testManager(t)
	got := m.plist()

	if got != m.plist() {
		t.Fatal("plist rendering is not deterministic")
	}
	if !strings.Contains(got, supervisorSh) {
		t.Errorf("plist does not run the supervisor %q", supervisorSh)
	}

	path := filepath.Join(dir, "out.plist")
	if err := os.WriteFile(path, []byte(got), 0644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("plutil", "-lint", path).CombinedOutput(); err != nil {
		t.Fatalf("plutil -lint: %v\n%s\n%s", err, out, got)
	}
}

// The install script runs once, as root, behind a password prompt — a syntax
// error there costs the user a prompt and gives back nothing. It also has to
// survive paths with spaces, since the core is installed from inside
// "…/nekkobox.app/Contents/Resources".
func TestInstallScriptIsValidShell(t *testing.T) {
	dir := t.TempDir()
	src := "/Users/mac13/My Apps/nekkobox.app/Contents/Resources/sing-box"
	got := installScript(src, filepath.Join(dir, "sup.sh"), filepath.Join(dir, "d.plist"))

	if !strings.Contains(got, shellQuote(src)) {
		t.Error("source binary path was not shell-quoted")
	}
	if !strings.Contains(got, shellQuote(installedBin)) {
		t.Errorf("install script does not install the core to %q", installedBin)
	}
	// bootout is asynchronous; bootstrapping before the old job is gone is
	// what produced "Bootstrap failed: 5: Input/output error".
	if !strings.Contains(got, "launchctl print") {
		t.Error("install script does not wait for the old job to unload")
	}

	path := filepath.Join(dir, "install.sh")
	if err := os.WriteFile(path, []byte(got), 0700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("/bin/sh", "-n", path).CombinedOutput(); err != nil {
		t.Fatalf("sh -n: %v\n%s\n%s", err, out, got)
	}
}

// The supervisor is what actually starts and stops the core, and it runs as
// root — a syntax error in it means "connect does nothing", with no way to
// see why short of reading the daemon log.
func TestSupervisorIsValidShell(t *testing.T) {
	m, dir := testManager(t)
	// A path with a space and a quote: the base dir really is
	// "~/Library/Application Support/nekkobox", so quoting is not academic.
	bin := "/Applications/Nekko's Box.app/Contents/Resources/sing-box"
	got := m.supervisor(bin)

	if got != m.supervisor(bin) {
		t.Fatal("supervisor rendering is not deterministic")
	}
	if !strings.Contains(got, `'/Applications/Nekko'\''s Box.app/Contents/Resources/sing-box'`) {
		t.Error("binary path was not shell-quoted")
	}
	if !strings.Contains(got, m.runFile()) {
		t.Errorf("supervisor does not watch the sentinel %q", m.runFile())
	}

	path := filepath.Join(dir, "supervisor.sh")
	if err := os.WriteFile(path, []byte(got), 0700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("/bin/sh", "-n", path).CombinedOutput(); err != nil {
		t.Fatalf("sh -n: %v\n%s\n%s", err, out, got)
	}
}
