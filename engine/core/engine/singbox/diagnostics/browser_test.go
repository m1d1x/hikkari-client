package diag

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// browserRecording is one row of testdata/hello/browsers.json: a hello a real
// browser sent (TestRecordBrowserHello), the core fingerprint that stands for
// that browser, and whether it is the browser's current stable release, which
// the fingerprint must then match.
type browserRecording struct {
	File    string `json:"file"`
	Browser string `json:"browser"`
	FP      string `json:"fp"`
	Stable  bool   `json:"stable"`
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
// browser it copies. A stable browser the fingerprint no longer matches fails
// the release with the fields that moved; other recordings are reported.
func TestCoreFingerprintsMatchBrowsers(t *testing.T) {
	for _, r := range browserRecordings(t) {
		browser := readHello(t, r.File)
		core := readHello(t, "tls-"+r.FP)
		diff := helloDiff(core, browser)
		if len(diff) == 0 {
			t.Logf("%s: fp=%s matches %s", r.File, r.FP, r.Browser)
			continue
		}
		var lines []string
		for _, f := range diff {
			lines = append(lines, f.Name+": core "+f.A+" | browser "+f.B)
		}
		msg := "fp_stale: fp=" + r.FP + " is not " + r.Browser + "\n  " + strings.Join(lines, "\n  ")
		if r.Stable {
			t.Error(msg)
		} else {
			t.Log(msg)
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
