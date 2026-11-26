package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The "авто" button picked the server within a Hikkari subscription and now
// sends sites silent on the direct path through the tunnel. A file written
// before keeps the button as the person left it, and the old server choice
// leaves with the first write.
func TestTheOldAutoButtonCarriesOverToTheAutoTunnel(t *testing.T) {
	for name, tc := range map[string]struct {
		file string
		want bool
	}{
		"auto on":       {`{"active":"a","auto":true,"autoSubscriptionId":"s","profiles":[]}`, true},
		"auto off":      {`{"active":"a","profiles":[]}`, false},
		"already moved": {`{"active":"a","auto":true,"autoTunnel":false,"profiles":[]}`, false},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "profiles.json")
			if err := os.WriteFile(path, []byte(tc.file), 0o600); err != nil {
				t.Fatal(err)
			}
			s := New(path)
			on, err := s.AutoTunnel()
			if err != nil {
				t.Fatal(err)
			}
			if on != tc.want {
				t.Fatalf("auto tunnel %v, want %v", on, tc.want)
			}
			pf, err := s.Read()
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Write(pf); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			again, err := New(path).AutoTunnel()
			if err != nil || again != tc.want {
				t.Fatalf("after a write: %v %v, file %s", again, err, raw)
			}
			for _, gone := range []string{`"auto":`, `"autoSubscriptionId"`} {
				if strings.Contains(string(raw), gone) {
					t.Fatalf("%s survived a write: %s", gone, raw)
				}
			}
		})
	}
}
