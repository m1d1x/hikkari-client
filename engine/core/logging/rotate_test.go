package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestRotateKeepsNumberedHistory(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.log")
	for i := 0; i < Keep+2; i++ {
		if err := os.WriteFile(p, []byte(fmt.Sprint(i)), 0644); err != nil {
			t.Fatal(err)
		}
		rotate(p)
	}
	for i := 1; i <= Keep; i++ {
		b, err := os.ReadFile(fmt.Sprintf("%s.%d", p, i))
		if err != nil {
			t.Fatalf(".%d missing: %v", i, err)
		}
		if want := fmt.Sprint(Keep + 2 - i); string(b) != want {
			t.Errorf(".%d = %q, want %q", i, b, want)
		}
	}
	if _, err := os.Stat(fmt.Sprintf("%s.%d", p, Keep+1)); err == nil {
		t.Errorf(".%d should not exist", Keep+1)
	}
}
