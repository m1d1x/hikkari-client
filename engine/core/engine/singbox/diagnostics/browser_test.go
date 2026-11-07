package diag

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// browserRecording is one row of testdata/hello/browsers.json: a hello a real
// browser sent (TestRecordBrowserHello), the core fingerprint that stands for
// that browser, and whether it is the browser's current stable release, which
// the fingerprint must then match. KnownStale names the fields in which the
// fingerprint is known to lag that release: written down when it is found,
// emptied when a core update catches up.
type browserRecording struct {
	File       string   `json:"file"`
	Browser    string   `json:"browser"`
	FP         string   `json:"fp"`
	Stable     bool     `json:"stable"`
	KnownStale []string `json:"known_stale"`
}

func browserRecordings(t *testing.T) []browserRecording {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "hello", "browsers.json"))
	if err != nil {
		t.Fatal(err)
	}
	var out []browserRecording
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestCoreFingerprintsMatchBrowsers is fp_stale of plan W4, run as the
// release's test: the release core's hello for a fingerprint (testdata/hello,
// pinned by TestReleaseCoreSendsTheRecordedHellos) against the hello of the
// browser it copies. For a stable browser the fields that differ must be the
// ones known_stale names: a new lag fails the release with the fields that
// moved, and so does a core update that closes one, so the row is brought up
// to date. Other recordings are reported.
func TestCoreFingerprintsMatchBrowsers(t *testing.T) {
	for _, r := range browserRecordings(t) {
		browser := readHello(t, r.File)
		core := readHello(t, "tls-"+r.FP)
		diff := helloDiff(core, browser)
		var names, lines []string
		for _, f := range diff {
			names = append(names, f.Name)
			lines = append(lines, f.Name+": core "+f.A+" | browser "+f.B)
		}
		if len(diff) > 0 {
			onlyCore, onlyBrowser := extsOnlyIn(core, browser)
			t.Logf("fp_stale: fp=%s is not %s (extensions only in the core %v, only in the browser %v)\n  %s",
				r.FP, r.Browser, onlyCore, onlyBrowser, strings.Join(lines, "\n  "))
		}
		known := append([]string(nil), r.KnownStale...)
		sort.Strings(known)
		sort.Strings(names)
		if r.Stable && !reflect.DeepEqual(names, known) && (len(names) > 0 || len(known) > 0) {
			t.Errorf("%s: fp=%s lags %s in %v, browsers.json knows %v", r.File, r.FP, r.Browser, names, known)
		}
	}
}

// What the recordings of 07.11 showed (research 2026-11-07-knob-truth-wire.md, W4):
// one browser build sends one hello, and the core's chrome differs from
// Chromium 152 in the signature algorithms alone, chrome_155 in one extension.
func TestBrowserRecordingsOf0711(t *testing.T) {
	a, b := readHello(t, "browser-chromium-152-pane-a"), readHello(t, "browser-chromium-152-pane-b")
	if d := helloDiff(a, b); len(d) != 0 || a.hk() != b.hk() {
		t.Fatalf("two connections of one build differ: %+v, %s %s", d, a.hk(), b.hk())
	}
	names := func(d []helloField) []string {
		var out []string
		for _, f := range d {
			out = append(out, f.Name)
		}
		return out
	}
	chrome := readHello(t, "tls-chrome")
	if got := names(helloDiff(chrome, a)); !reflect.DeepEqual(got, []string{"sigalgs"}) {
		t.Fatalf("chrome vs Chromium 152: %v", got)
	}
	// Chromium 152 offers GREASE and ML-DSA (0x0904-0x0906) first; uTLS's
	// Chrome 133 offers neither.
	if f := helloFields(a)["sigalgs"]; !strings.HasPrefix(f, "0a0a,0904,0905,0906,") {
		t.Fatalf("browser sigalgs %s", f)
	}
	c155 := readHello(t, "tls-chrome_155")
	if got := names(helloDiff(c155, a)); !reflect.DeepEqual(got, []string{"extensions"}) {
		t.Fatalf("chrome_155 vs Chromium 152: %v", got)
	}
	if onlyCore, onlyBrowser := extsOnlyIn(c155, a); !reflect.DeepEqual(onlyCore, []string{"ca34"}) || len(onlyBrowser) != 0 {
		t.Fatalf("extensions only in chrome_155 %v, only in the browser %v", onlyCore, onlyBrowser)
	}
}

func TestHelloDiffIgnoresWhatChangesPerConnection(t *testing.T) {
	// Two recordings of the core's chrome: other extension order, other ECH
	// and padding lengths.
	if d := helloDiff(readHello(t, "tls-chrome"), readHello(t, "tls-chrome-again")); len(d) != 0 {
		t.Fatalf("%+v", d)
	}
	// classical drops ML-KEM from the groups as well as from the shares.
	if d := helloDiff(readHello(t, "tls-chrome"), readHello(t, "reality-chrome-classical")); len(d) != 2 || d[0].Name != "groups" || d[1].Name != "shares" {
		t.Fatalf("classical key_share: %+v", d)
	}
}

// What Chrome 157 and Firefox 159 showed (07.11): chrome_155 lacks one
// extension of Chrome 157 (0x12e0; 0xca34 is Chrome's own), chrome lacks it
// too and the ML-DSA signature algorithms; firefox still offers FFDHE and two
// CBC suites Firefox 159 dropped, and lacks session_ticket and
// psk_key_exchange_modes. chrome-headless-shell is not the browser: its hello
// is Chromium 152's.
func TestBrowserRecordingsOfStable0711(t *testing.T) {
	c157 := readHello(t, "browser-chrome-157-a")
	if d := helloDiff(c157, readHello(t, "browser-chrome-157-b")); len(d) != 0 {
		t.Fatalf("two runs of Chrome 157 differ: %+v", d)
	}
	if onlyCore, onlyBrowser := extsOnlyIn(readHello(t, "tls-chrome_155"), c157); len(onlyCore) != 0 || !reflect.DeepEqual(onlyBrowser, []string{"12e0"}) {
		t.Fatalf("chrome_155 vs Chrome 157: only core %v, only browser %v", onlyCore, onlyBrowser)
	}
	if shell := readHello(t, "browser-chrome-157-shell"); shell.hk() != readHello(t, "browser-chromium-152-pane-a").hk() || shell.hk() == c157.hk() {
		t.Fatalf("headless shell %s, Chrome 157 %s", shell.hk(), c157.hk())
	}
	ff := readHello(t, "browser-firefox-159-a")
	core := helloFields(readHello(t, "tls-firefox"))
	if f := helloFields(ff); f["groups"] != "x25519mlkem768,x25519,p256,p384,p521" || !strings.Contains(core["groups"], "ffdhe2048") {
		t.Fatalf("firefox groups: core %s, browser %s", core["groups"], f["groups"])
	}
	if onlyCore, onlyBrowser := extsOnlyIn(readHello(t, "tls-firefox"), ff); len(onlyCore) != 0 || !reflect.DeepEqual(onlyBrowser, []string{"0023", "002d"}) {
		t.Fatalf("firefox vs Firefox 159: only core %v, only browser %v", onlyCore, onlyBrowser)
	}
}
