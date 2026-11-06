package vpn

import (
	"path/filepath"
	"testing"
)

// The macOS check looked for the core beside the GUI, in Contents/MacOS, and
// never found it (tester-004, 06.11: "config not checked: no core at …").
func TestCoreBinaryForLayout(t *testing.T) {
	mac := coreBinaryFor("/Applications/hikkari.app/Contents/MacOS/nekkobox", "darwin")
	if want := filepath.Join("/Applications/hikkari.app/Contents/Resources", BinaryName); mac != want {
		t.Fatalf("darwin: %s, want %s", mac, want)
	}
	win := coreBinaryFor(filepath.Join("root", "app", "nekkobox.exe"), "windows")
	if want := filepath.Join("root", "app", BinaryName); win != want {
		t.Fatalf("windows: %s, want %s", win, want)
	}
}
