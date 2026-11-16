package presets

import (
	"strings"
	"testing"
)

// browserProcesses are general-purpose browsers by the process name a rule
// would match. A service group names the service's own app; a browser is the
// whole web, and routing it by process takes every site it opens to the
// group's side. With the group past the VPN that means every blocked site
// stalls until the auto tunnel proves it silent (plans/2026-11-07-browser-
// out-of-presets-autotunnel-sites.md). Windows names are matched without case,
// as the system does.
var browserProcesses = map[string][]string{
	"windows": {"chrome.exe", "msedge.exe", "firefox.exe", "browser.exe", "opera.exe", "brave.exe", "vivaldi.exe", "chromium.exe", "iexplore.exe"},
	"darwin":  {"Google Chrome", "Google Chrome Helper", "Safari", "Firefox", "Microsoft Edge", "Yandex", "Yandex Helper", "Opera", "Brave Browser", "Vivaldi", "Chromium", "Arc"},
	"linux":   {"google-chrome", "chrome", "chromium", "chromium-browser", "firefox", "yandex-browser", "opera", "brave", "brave-browser", "vivaldi", "microsoft-edge"},
}

func isBrowser(platform, process string) bool {
	for _, b := range browserProcesses[platform] {
		if process == b || (platform == "windows" && strings.EqualFold(process, b)) {
			return true
		}
	}
	return false
}

func TestBundledCatalogRoutesNoBrowserByProcess(t *testing.T) {
	c, err := Parse(bundled)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range c.Services {
		for platform, processes := range s.Processes {
			for _, p := range processes {
				if isBrowser(platform, p) {
					t.Errorf("%s routes the browser %s/%q: a group names its service's app, not the web", s.ID, platform, p)
				}
			}
		}
	}
}

func TestACatalogNamesOnlyAKnownDefaultMode(t *testing.T) {
	doc := func(mode string) []byte {
		return []byte(`{"schemaVersion":1,"version":1` + mode + `,"services":[{"id":"a","name":"A","icon":"","domains":["a.example"],"ips":[],"processes":{},"defaultModes":[]}]}`)
	}
	for mode, want := range map[string]string{``: "", `,"defaultMode":"proxy"`: "proxy", `,"defaultMode":"bypass"`: "bypass"} {
		c, err := Parse(doc(mode))
		if err != nil || string(c.DefaultMode) != want {
			t.Fatalf("%q: got %q, %v", mode, c.DefaultMode, err)
		}
	}
	if _, err := Parse(doc(`,"defaultMode":"all"`)); err == nil {
		t.Fatal("an unknown default mode was taken")
	}
}
