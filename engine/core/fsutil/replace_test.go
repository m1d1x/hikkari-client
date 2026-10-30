package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceSwapsContentWhileTargetIsOpen(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "state.json")
	if err := os.WriteFile(dst, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(dir, ".state-tmp")
	if err := os.WriteFile(tmp, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- Replace(tmp, dst) }()
	// A short-lived reader, like a background worker's ReadFile.
	reader.Close()
	if err := <-done; err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if got, _ := os.ReadFile(dst); string(got) != "new" {
		t.Fatalf("content = %q, want new", got)
	}
}
