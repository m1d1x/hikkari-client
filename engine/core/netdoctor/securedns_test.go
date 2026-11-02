package netdoctor

import "testing"

func TestParseSecureDNS(t *testing.T) {
	cases := []struct {
		name, raw, mode, resolver string
	}{
		{"never changed", `{"browser":{}}`, "automatic", ""},
		{"strict cloudflare", `{"dns_over_https":{"mode":"secure","templates":"https://chrome.cloudflare-dns.com/dns-query"}}`, "secure", "chrome.cloudflare-dns.com"},
		{"off", `{"dns_over_https":{"mode":"off"}}`, "off", ""},
		{"a private resolver stays private", `{"dns_over_https":{"mode":"secure","templates":"https://doh.my-home.example/q"}}`, "secure", "custom"},
		{"an account in the path is dropped", `{"dns_over_https":{"mode":"secure","templates":"https://chromium.dns.nextdns.io/abc123"}}`, "secure", "chromium.dns.nextdns.io"},
		{"damaged file", `{`, "unknown", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseSecureDNS("chrome", []byte(c.raw))
			if got.Mode != c.mode || got.Resolver != c.resolver {
				t.Fatalf("got %+v", got)
			}
		})
	}
}
