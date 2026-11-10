package diag

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"NekkoVPN/core/logging"
	"NekkoVPN/core/netinfo"
)

// lesha-001, 10.11: Dota's relay pings went out and some never came back, but the
// log said only [REDACTED_IP]: which relays stayed silent could not be told.
func TestADestinationIsNamedByNetworkAndAStableIDWithoutItsAddress(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	a := destLabel(key, "155.133.248.34")
	if a != destLabel(key, "155.133.248.34") {
		t.Fatal("the same address must get the same id")
	}
	if a == destLabel(key, "155.133.248.35") {
		t.Fatal("two addresses must get different ids")
	}
	if a == destLabel([]byte("another key, another install!!!!"), "155.133.248.34") {
		t.Fatal("the id must depend on the install's key")
	}
	// A Valve relay abroad: the embedded database knows only Russian networks.
	if !regexp.MustCompile(`^ext#[0-9a-f]{8}$`).MatchString(a) {
		t.Fatalf("got %q", a)
	}
	// Yandex DNS: a Russian network is named.
	if ru := destLabel(key, "77.88.8.8"); netinfo.Available() && !regexp.MustCompile(`^AS\d+#[0-9a-f]{8}$`).MatchString(ru) {
		t.Fatalf("got %q", ru)
	}
	line := "[diag:conn] udp 192.168.1.66:5000 → 155.133.248.34:27015 dst=" + a + " host=\"\""
	if got := logging.SanitizeLog(line); !strings.Contains(got, "dst="+a+" ") || strings.Contains(got, "155.133") {
		t.Fatalf("the label must survive and the address must not: %q", got)
	}
}

func TestLocalDestinationsAreClassedNotIdentified(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	for addr, want := range map[string]string{
		"":             "-",
		"127.0.0.1":    "loopback",
		"192.168.1.1":  "private",
		"10.8.0.1":     "private",
		"100.64.3.4":   "private",
		"fe80::1":      "private",
		"198.18.0.7":   "fakeip",
		"not-an-ip":    "-",
		"172.19.0.2":   "private",
		"169.254.1.10": "private",
	} {
		if got := destLabel(key, addr); got != want {
			t.Fatalf("destLabel(%q) = %q, want %q", addr, got, want)
		}
	}
}

func TestTheKeyIsKeptForTheInstall(t *testing.T) {
	dir := t.TempDir()
	r1 := New(nil, filepath.Join(dir, "config.json"))
	r2 := New(nil, filepath.Join(dir, "config.json"))
	if r1.dest("155.133.248.34") != r2.dest("155.133.248.34") {
		t.Fatal("ids must hold across restarts")
	}
	st, err := os.Stat(filepath.Join(dir, destKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	// Windows has no owner-only mode bits: the key sits in the user's own
	// profile directory there.
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode %v, want 0600", st.Mode().Perm())
	}
}
