package appversion

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileFor(t *testing.T) {
	mac := FileFor("/Applications/hikkari.app/Contents/MacOS/nekkobox", "darwin")
	if want := filepath.FromSlash("/Applications/hikkari.app/Contents/Resources/VERSION"); mac != want {
		t.Fatalf("darwin: %s, want %s", mac, want)
	}
	win := FileFor(filepath.Join("root", "app", "nekkobox.exe"), "windows")
	if want := filepath.Join("root", "app", "VERSION"); win != want {
		t.Fatalf("windows: %s, want %s", win, want)
	}
}

func TestRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "VERSION")
	if got := Read(path); got != "" {
		t.Fatalf("missing file: %q", got)
	}
	if err := os.WriteFile(path, []byte("1.0.51\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Read(path); got != "1.0.51" {
		t.Fatalf("got %q", got)
	}
}
