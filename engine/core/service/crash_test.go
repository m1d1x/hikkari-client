package service

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"

	"NekkoVPN/core/logging"
)

func TestCaptureCrashesMovesThePreviousTraceToTheLog(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, crashFile), []byte("panic: boom\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	log := logging.New(filepath.Join(dir, "service.log"))
	defer log.Close()

	// Windows cannot remove the temporary directory while the file is held.
	t.Cleanup(func() { _ = debug.SetCrashOutput(nil, debug.CrashOptions{}) })
	captureCrashes(dir, log)

	if !strings.Contains(log.Tail(), "panic: boom") {
		t.Fatalf("trace not in the log: %q", log.Tail())
	}
	content, err := os.ReadFile(filepath.Join(dir, crashFile))
	if err != nil {
		t.Fatal(err)
	}
	if len(content) != 0 {
		t.Fatalf("old trace stayed in the crash file: %q", content)
	}

	// A clean previous run adds nothing.
	captureCrashes(dir, log)
	if n := strings.Count(log.Tail(), "the previous run crashed"); n != 1 {
		t.Fatalf("reported %d crashes, want 1", n)
	}
}
