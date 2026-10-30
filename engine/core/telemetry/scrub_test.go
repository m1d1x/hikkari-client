package telemetry

import (
	"strings"
	"testing"
)

// corpus holds log lines shaped like what sing-box, Xray, the service, the
// updater and the engine actually write, with documentation-range addresses
// and made-up keys. Each case lists the secrets that must not survive.
var corpus = []struct {
	in     string
	secret []string
	keep   []string
}{
	{
		in:     `ERROR [3201ms] outbound/vless[p-Frankfurt]: dial tcp 203.0.113.7:443: i/o timeout`,
		secret: []string{"203.0.113.7", ":443"},
		keep:   []string{"outbound/vless", "i/o timeout"},
	},
	{
		in:     `connection: open outbound connection to [2001:db8:5::17]:8443 failed`,
		secret: []string{"2001:db8:5::17", "8443"},
		keep:   []string{"failed"},
	},
	{
		in:     `import: vless://5b0e3c1a-9f2d-4c8e-b1a7-0d6e2f3a4b5c@edge.example.org:443?security=reality&pbk=Zq3kV9xP2mN7bR4tY8wE1uI6oA5sD0fG3hJ2kL9zX7c&sid=6ba85179e30d4fc2#My%20Server`,
		secret: []string{"5b0e3c1a", "edge.example.org", "Zq3kV9xP2mN7bR4tY8wE1uI6oA5sD0fG3hJ2kL9zX7c", "6ba85179e30d4fc2", "My%20Server"},
		keep:   []string{"vless://<url>"},
	},
	{
		in:     `sync: GET https://sub.hikkari.net/sub/q7Wm2xLp9Rt4Kv8Nc3Bz6Yh1 → 200`,
		secret: []string{"q7Wm2xLp9Rt4Kv8Nc3Bz6Yh1"},
		keep:   []string{"https://sub.hikkari.net/sub/<secret>", "200"},
	},
	{
		in:     `sync: GET https://provider.example.com/api/v1/client/subscribe?token=abc123def456 failed: 403`,
		secret: []string{"provider.example.com", "abc123def456"},
		keep:   []string{"https://<url>", "403"},
	},
	{
		in:     `hysteria2: auth=hunter2pass obfs password: s3cr3t-0bfs pinSHA256=8f434346648f6b96df89dda901c5176b10a6d83961dd3c1ac88b59b2dc327aa4`,
		secret: []string{"hunter2pass", "s3cr3t-0bfs", "8f434346648f6b96"},
	},
	{
		in:     `open /Users/alice/Library/Application Support/nekkobox/profiles.json: permission denied`,
		secret: []string{"alice"},
		keep:   []string{"~/Library/Application Support/nekkobox/profiles.json", "permission denied"},
	},
	{
		in:     `open C:\Users\Bob Smith\AppData\Local\Hikkari\routing.json: access is denied`,
		secret: []string{"Bob"},
		keep:   []string{`\AppData\Local\Hikkari\routing.json`},
	},
	{
		in:     `dns: exchange failed for youtube.com via 1.1.1.1: context deadline exceeded`,
		secret: []string{"youtube.com", "1.1.1.1"},
		keep:   []string{"context deadline exceeded"},
	},
	{
		in:     `xray: reality: private_key: "uE3c9KpX2mQ7vR4tY8wN1bI6oA5sD0fG3hJ2kL9zX7c" short_id 6ba85179`,
		secret: []string{"uE3c9KpX2mQ7vR4tY8wN1bI6oA5sD0fG3hJ2kL9zX7c"},
	},
	{
		in:   `updater: fetch https://update.hikkari.net/stable/macos/manifest.json: 404`,
		keep: []string{"https://update.hikkari.net/stable/macos/manifest.json"},
	},
	{
		in:   `panic: runtime error at core/api/methods.go:473 and lib/state/app_state.dart:120 at 12:30:45`,
		keep: []string{"core/api/methods.go:473", "lib/state/app_state.dart:120", "12:30:45"},
	},
	{
		in:   `service: pipe \\.\pipe\hikkari-service: The system cannot find the file specified.`,
		keep: []string{`\\.\pipe\hikkari-service`},
	},
	{
		in:     `tun: route 198.51.100.0/24 via utun4, fe80::1%en0 link-local`,
		secret: []string{"198.51.100.0", "fe80::1"},
		keep:   []string{"utun4"},
	},
}

func TestScrubRemovesEverySecretOfTheCorpus(t *testing.T) {
	for _, c := range corpus {
		got := Scrub(c.in)
		for _, s := range c.secret {
			if strings.Contains(got, s) {
				t.Errorf("secret %q survived:\n in: %s\nout: %s", s, c.in, got)
			}
		}
		for _, k := range c.keep {
			if !strings.Contains(got, k) {
				t.Errorf("lost %q:\n in: %s\nout: %s", k, c.in, got)
			}
		}
	}
}

func TestScrubIsIdempotent(t *testing.T) {
	for _, c := range corpus {
		once := Scrub(c.in)
		if twice := Scrub(once); twice != once {
			t.Errorf("second pass changed the line:\n%s\n%s", once, twice)
		}
	}
}

func TestFingerprintFoldsNumbersAndKeepsCodesApart(t *testing.T) {
	a := Fingerprint("engine.start", Scrub("retry 3 of 5: dial 203.0.113.7:443"))
	b := Fingerprint("engine.start", Scrub("retry 4 of 5: dial 198.51.100.9:8443"))
	if a != b {
		t.Fatalf("same failure, different fingerprints: %s %s", a, b)
	}
	if c := Fingerprint("tunnel.dns", Scrub("retry 3 of 5: dial 203.0.113.7:443")); c == a {
		t.Fatal("different codes share a fingerprint")
	}
}
