package diag

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// wireGolden is what the release's core sends for each case of wireCases:
// sing-box-lx v1.14.3-lx.21-hk.1 (revision c06e742b5717, uTLS fork of
// metacubex/utls v1.8.7), recorded by TestRecordWireHellos on darwin/arm64;
// the cores of f350f1e and of 1.0.54-1.0.55 testers on Windows sent the same
// rows. A core update that changes a hello changes a row here; that is the
// point (plan section 7). reference.json beside the hellos holds the plain
// TLS rows with the revision they were recorded from.
var wireGolden = map[string]struct{ ja4, hk string }{
	"tls-chrome":               {"t13d1516h2_8daaf6152771_d8a2da3f94cd", "h1e431ec7444af069a"},
	"tls-chrome-again":         {"t13d1516h2_8daaf6152771_d8a2da3f94cd", "h1e431ec7444af069a"},
	"tls-chrome-alpn-http11":   {"t13d1516h1_8daaf6152771_d8a2da3f94cd", "h1e431ec7444af069a"},
	"tls-chrome_155":           {"t13d1517h2_8daaf6152771_cb7bf5808d99", "h1121e2b99d2744911"},
	"tls-firefox":              {"t13d1715h2_5b57614c22b0_a54fffd0eb61", "h1a7f29bb57cd8c350"},
	"tls-edge":                 {"t13d1515h2_8daaf6152771_de4a06bb82e3", "h186e519f72a3bd01a"},
	"tls-safari":               {"t13d2013h2_a09f3c656075_7f0f34a4126d", "h1c4b9dfb6a023e46d"},
	"tls-ios":                  {"t13d2613h2_2802a3db6c62_845d286b0d67", "h122df09a9d9832ecc"},
	"tls-android":              {"t12d120700_d34a8e72043a_036209cd1ead", "h1b16c973fd5fb56a7"},
	"tls-360":                  {"t12d2010s2_0bf03fa604e3_736b2a1ed4d3", "h1a028e2f5c9a5dc21"},
	"tls-qq":                   {"t13d1516h2_8daaf6152771_e5627efa2ab1", "h10afd45ddf31c8b3a"},
	"tls-go":                   {"t13d131100_f57a46bbacb6_ab7e3b40a677", "h11c409d9ffa0e9507"},
	"reality-chrome":           {"t13d1516h2_8daaf6152771_d8a2da3f94cd", "h1e431ec7444af069a"},
	"reality-chrome_155":       {"t13d1517h2_8daaf6152771_cb7bf5808d99", "h1121e2b99d2744911"},
	"reality-chrome-classical": {"t13d1516h2_8daaf6152771_d8a2da3f94cd", "h13ba089e2b415f898"},
	"xhttp-chrome":             {"t13d1516h2_8daaf6152771_d8a2da3f94cd", "h1e431ec7444af069a"},
}

func readHello(t *testing.T, name string) *clientHello {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "hello", name+".bin"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := parseClientHello(b)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return h
}

func TestRecordedHellosKeepTheirFingerprints(t *testing.T) {
	// randomized is drawn anew by every core process: no row stands for it.
	if len(wireGolden) != len(wireCases())-1 {
		t.Fatalf("%d golden rows for %d cases", len(wireGolden), len(wireCases()))
	}
	for name, want := range wireGolden {
		h := readHello(t, name)
		if got := h.ja4(); got != want.ja4 {
			t.Errorf("%s: ja4 %s, want %s", name, got, want.ja4)
		}
		if got := h.hk(); got != want.hk {
			t.Errorf("%s: hk %s, want %s", name, got, want.hk)
		}
	}
}

// The public JA4 of Chrome since it took the new ALPS codepoint: had the parser
// read the hello wrong, this would not come out.
func TestChromeMatchesItsPublicJA4(t *testing.T) {
	if got := readHello(t, "tls-chrome").ja4(); got != "t13d1516h2_8daaf6152771_d8a2da3f94cd" {
		t.Fatal(got)
	}
}

func TestHKIgnoresWhatChangesPerConnection(t *testing.T) {
	a, b := readHello(t, "tls-chrome"), readHello(t, "tls-chrome-again")
	// The ECH GREASE length and the extension order are drawn per connection.
	if a.Len == b.Len && order(a) == order(b) {
		t.Fatal("the two recordings are alike: the test proves nothing")
	}
	if a.hk() != b.hk() {
		t.Fatal("hk follows the shuffle or the GREASE")
	}
	// Reality hides its auth in the session id; the hello is the fingerprint's.
	if readHello(t, "reality-chrome").hk() != a.hk() || readHello(t, "xhttp-chrome").hk() != a.hk() {
		t.Fatal("hk differs between Reality, XHTTP and TLS on one fingerprint")
	}
	// ALPN is a knob reported apart.
	if readHello(t, "tls-chrome-alpn-http11").hk() != a.hk() {
		t.Fatal("hk follows ALPN")
	}
}

func order(h *clientHello) string {
	var b strings.Builder
	for _, e := range h.Exts {
		b.WriteString(hex4(grease(e.Type)))
	}
	return b.String()
}

// The reason hk exists: JA4 does not see the groups, and the cut of 03–06.11
// tracked the groups.
func TestHKSeesWhatJA4Misses(t *testing.T) {
	hybrid, classical := readHello(t, "reality-chrome"), readHello(t, "reality-chrome-classical")
	if hybrid.ja4() != classical.ja4() {
		t.Fatal("JA4 now sees the key share: revisit the case for hk")
	}
	if hybrid.hk() == classical.hk() {
		t.Fatal("hk misses the key share")
	}
	if !strings.Contains(hybrid.fields(), "shares=grease,x25519mlkem768,x25519 ") || !strings.Contains(classical.fields(), "shares=grease,x25519 ") {
		t.Fatalf("%s\n%s", hybrid.fields(), classical.fields())
	}
}

func TestEveryFingerprintSendsItsOwnHello(t *testing.T) {
	seen := map[string]string{}
	for _, name := range []string{"tls-chrome", "tls-chrome_155", "tls-firefox", "tls-edge", "tls-safari", "tls-ios", "tls-android", "tls-360", "tls-qq", "tls-randomized", "tls-go"} {
		// randomized is in: whatever it drew, it must not be a named one.
		hk := readHello(t, name).hk()
		if other, dup := seen[hk]; dup {
			t.Errorf("%s sends the hello of %s", name, other)
		}
		seen[hk] = name
	}
}

// A TLS profile with no fingerprint sends Go's own hello: no ALPN, no GREASE.
func TestNoFingerprintIsGosHello(t *testing.T) {
	f := readHello(t, "tls-go").fields()
	for _, want := range []string{"alpn_sent=- ", "grease=0 "} {
		if !strings.Contains(f, want) {
			t.Fatalf("want %q in %s", want, f)
		}
	}
}

func TestFieldsNameNoSecret(t *testing.T) {
	for name := range wireCases() {
		f := readHello(t, name).fields()
		if strings.Contains(f, wireSNI) {
			t.Fatalf("%s: the line carries the server name: %s", name, f)
		}
		if !regexp.MustCompile(`^(\S+=\S+ ?)+$`).MatchString(f) {
			t.Fatalf("%s: not one token per field: %s", name, f)
		}
	}
}

// records splits a handshake message into TLS records of at most n bytes.
func records(msg []byte, n int) []byte {
	var out []byte
	for len(msg) > 0 {
		k := min(n, len(msg))
		out = append(out, 22, 3, 1)
		out = binary.BigEndian.AppendUint16(out, uint16(k))
		out = append(out, msg[:k]...)
		msg = msg[k:]
	}
	return out
}

func TestReadClientHelloJoinsRecords(t *testing.T) {
	msg, err := os.ReadFile(filepath.Join("testdata", "hello", "tls-chrome_155.bin"))
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{1 << 14, 1000, 3} {
		got, n, err := readClientHello(bytes.NewReader(append(records(msg, size), 23, 3, 3, 0, 1, 0)))
		if err != nil || !bytes.Equal(got, msg) {
			t.Fatalf("records of %d: %v", size, err)
		}
		if want := (len(msg) + size - 1) / size; n != want {
			t.Fatalf("records of %d: counted %d, want %d", size, n, want)
		}
	}
}

func TestReadClientHelloRefusesWhatIsNotOne(t *testing.T) {
	msg, _ := os.ReadFile(filepath.Join("testdata", "hello", "tls-chrome.bin"))
	for name, in := range map[string][]byte{
		"cut":         records(msg, 1<<14)[:100],
		"application": {23, 3, 3, 0, 1, 0},
		"server":      records(append([]byte{2}, msg[1:]...), 1<<14),
		"empty":       nil,
	} {
		if _, _, err := readClientHello(bytes.NewReader(in)); err == nil {
			t.Errorf("%s: read as a hello", name)
		}
	}
}

// Any cut of a real hello is refused or read, never a panic.
func TestParseClientHelloSurvivesEveryCut(t *testing.T) {
	msg, _ := os.ReadFile(filepath.Join("testdata", "hello", "tls-firefox.bin"))
	for i := 0; i < len(msg); i++ {
		_, _ = parseClientHello(msg[:i])
		broken := append([]byte(nil), msg...)
		broken[i] ^= 0xff
		_, _ = parseClientHello(broken)
	}
}

// TestReleaseCoreSendsTheRecordedHellos is the golden test of plan section 7:
// the core in HIKKARI_TEST_CORE, the one the release ships, must send the
// recorded hellos. The hello depends on the core's revision and the config
// alone, so this test stands for every tester on that release. When it fails
// after a core update, the hello changed: record anew (TestRecordWireHellos)
// and check the new rows against the browsers before shipping.
func TestReleaseCoreSendsTheRecordedHellos(t *testing.T) {
	binary := os.Getenv("HIKKARI_TEST_CORE")
	if binary == "" {
		t.Skip("HIKKARI_TEST_CORE is not set")
	}
	rev := coreRevision(context.Background(), binary)
	if !regexp.MustCompile(`^[0-9a-f]{12}$`).MatchString(rev) {
		t.Fatalf("the core names no revision: %q", rev)
	}
	t.Logf("core %s", rev)
	for name, ob := range wireCases() {
		if name == "tls-randomized" {
			continue // seeded per process
		}
		msg, _, err := mirrorHello(context.Background(), binary, ob)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		h, err := parseClientHello(msg)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if want := wireGolden[name]; h.ja4() != want.ja4 || h.hk() != want.hk {
			t.Errorf("%s: ja4=%s hk=%s, recorded ja4=%s hk=%s", name, h.ja4(), h.hk(), want.ja4, want.hk)
		}
		if !h.SNI {
			t.Errorf("%s: no SNI in the hello", name)
		}
	}
}
