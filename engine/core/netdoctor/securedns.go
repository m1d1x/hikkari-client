package netdoctor

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// BrowserSecureDNS is a browser's Secure DNS setting, read from its own files.
// Nothing here changes them.
type BrowserSecureDNS struct {
	Browser string `json:"browser"`
	// Mode is "off", "automatic" (falls back to the system resolver) or
	// "secure" (does not: a dead resolver means no site opens).
	Mode string `json:"mode"`
	// Resolver is the host of a known public resolver, "custom" for anyone
	// else's, "" when the browser picks by itself.
	Resolver string `json:"resolver,omitempty"`
}

// chromiumProfiles are where Chromium browsers keep their "Local State",
// relative to the per-user application data folder.
var chromiumProfiles = map[string][]struct{ browser, dir string }{
	"windows": {
		{"chrome", `Google\Chrome\User Data`},
		{"edge", `Microsoft\Edge\User Data`},
		{"brave", `BraveSoftware\Brave-Browser\User Data`},
		{"yandex", `Yandex\YandexBrowser\User Data`},
	},
	"darwin": {
		{"chrome", "Google/Chrome"},
		{"edge", "Microsoft Edge"},
		{"brave", "BraveSoftware/Brave-Browser"},
		{"yandex", "Yandex/YandexBrowser"},
	},
}

// SecureDNSSettings lists the installed Chromium browsers and their settings.
// The result is non-nil: empty means none is installed.
func SecureDNSSettings() []BrowserSecureDNS {
	base := os.Getenv("LOCALAPPDATA")
	if runtime.GOOS == "darwin" {
		if home, err := os.UserHomeDir(); err == nil {
			base = filepath.Join(home, "Library", "Application Support")
		}
	}
	out := []BrowserSecureDNS{}
	if base == "" {
		return out
	}
	for _, p := range chromiumProfiles[runtime.GOOS] {
		raw, err := os.ReadFile(filepath.Join(base, p.dir, "Local State"))
		if err != nil {
			continue
		}
		out = append(out, parseSecureDNS(p.browser, raw))
	}
	return out
}

// parseSecureDNS reads the setting out of a "Local State" file. A browser that
// never had it changed has no such key and runs on its default, automatic.
func parseSecureDNS(browser string, raw []byte) BrowserSecureDNS {
	var state struct {
		DoH struct {
			Mode      string `json:"mode"`
			Templates string `json:"templates"`
		} `json:"dns_over_https"`
	}
	res := BrowserSecureDNS{Browser: browser, Mode: "automatic"}
	if err := json.Unmarshal(raw, &state); err != nil {
		res.Mode = "unknown"
		return res
	}
	if state.DoH.Mode != "" {
		res.Mode = state.DoH.Mode
	}
	res.Resolver = resolverClass(state.DoH.Templates)
	return res
}

// resolverClass names a public resolver and hides anything else: a template
// can carry a personal account, as NextDNS's do in their path.
func resolverClass(templates string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(templates), " ")
	if first == "" {
		return ""
	}
	u, err := url.Parse(first)
	if err != nil {
		return "custom"
	}
	host := strings.ToLower(u.Hostname())
	for _, public := range []string{"cloudflare-dns.com", "dns.google", "quad9.net", "opendns.com", "cleanbrowsing.org", "adguard-dns.com", "nextdns.io"} {
		if host == public || strings.HasSuffix(host, "."+public) {
			return host
		}
	}
	return "custom"
}
