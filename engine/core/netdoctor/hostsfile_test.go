package netdoctor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const hostsWithOverride = "# my notes\r\n127.0.0.1 steamcommunity.com\r\n10.0.0.5 nas.local\r\n"

func writeHosts(t *testing.T, content string) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, "hosts")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

func entries(t *testing.T, dir string) []string {
	t.Helper()
	list, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range list {
		names = append(names, e.Name())
	}
	return names
}

func TestDisableHostsOverridesKeepsACopyAndLeavesNoTemporaryFile(t *testing.T) {
	dir, path := writeHosts(t, hostsWithOverride)
	now := time.Unix(1700000000, 0)

	n, err := DisableHostsOverrides(path, now)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := FilterHosts([]byte(hostsWithOverride))
	if string(got) != string(want) {
		t.Fatalf("hosts = %q, want %q", got, want)
	}
	backup, err := os.ReadFile(fmt.Sprintf("%s.hikkari-%d.bak", path, now.Unix()))
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != hostsWithOverride {
		t.Fatalf("backup = %q", backup)
	}
	if names := entries(t, dir); len(names) != 2 {
		t.Fatalf("left behind: %v", names)
	}
}

func TestDisableHostsOverridesKeepsTheFileReadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits do not apply")
	}
	_, path := writeHosts(t, hostsWithOverride)
	if _, err := DisableHostsOverrides(path, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("mode = %v: the resolver must still read it", info.Mode().Perm())
	}
}

func TestDisableHostsOverridesWritesNothingWithoutOverrides(t *testing.T) {
	dir, path := writeHosts(t, "10.0.0.5 nas.local\n")
	n, err := DisableHostsOverrides(path, time.Unix(1, 0))
	if err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if names := entries(t, dir); len(names) != 1 {
		t.Fatalf("wrote: %v", names)
	}
}

func TestDisableHostsOverridesWithoutAFileIsNotAnError(t *testing.T) {
	if n, err := DisableHostsOverrides(filepath.Join(t.TempDir(), "hosts"), time.Unix(1, 0)); err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

// No copy, no change. The refusal comes from the directory, so the replace
// of the hosts file itself would be refused too; the point here is that the
// error reaches the caller, the file is intact and the log gets no path.
func TestDisableHostsOverridesFailsCleanlyWhenItCannotWriteBeside(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory this user cannot write to")
	}
	dir, path := writeHosts(t, hostsWithOverride)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	n, err := DisableHostsOverrides(path, time.Unix(1, 0))
	if err == nil || n != 0 || !strings.Contains(err.Error(), "backing up hosts") {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if strings.Contains(err.Error(), dir) {
		t.Fatalf("path in the error: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != hostsWithOverride {
		t.Fatalf("hosts changed: %q", got)
	}
}

func TestDisableHostsOverridesReadErrorCarriesNoPath(t *testing.T) {
	dir := t.TempDir() // a directory in place of the file
	_, err := DisableHostsOverrides(dir, time.Unix(1, 0))
	if err == nil || strings.Contains(err.Error(), dir) {
		t.Fatalf("err = %v", err)
	}
}

func TestReplaceFileLeavesTheOldFileWhenTheReplaceFails(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	// A non-empty directory cannot be renamed over.
	if err := os.MkdirAll(filepath.Join(target, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceFile(target, []byte("new"), 0o644); err == nil {
		t.Fatal("no error")
	}
	if names := entries(t, dir); len(names) != 1 {
		t.Fatalf("temporary file left behind: %v", names)
	}
}

func TestBareFileErrorDropsThePath(t *testing.T) {
	_, err := os.ReadFile(filepath.Join(t.TempDir(), "secret-name"))
	if got := BareFileError(err); strings.Contains(got.Error(), "secret-name") || !errors.Is(got, os.ErrNotExist) {
		t.Fatalf("%v", got)
	}
	other := errors.New("plain")
	if BareFileError(other) != other || BareFileError(nil) != nil {
		t.Fatal("non-file errors must pass through")
	}
}
