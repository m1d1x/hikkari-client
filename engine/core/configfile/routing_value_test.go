package configfile

import "testing"

func TestNormalizeIPValue(t *testing.T) {
	ok := map[string]string{
		"1.2.3.4":         "1.2.3.4/32",
		" 1.2.3.4 ":       "1.2.3.4/32",
		"192.168.0.0/16":  "192.168.0.0/16",
		"192.168.13.7/16": "192.168.0.0/16", // masked to the canonical form
		"2001:db8::1":     "2001:db8::1/128",
		"2001:db8::/32":   "2001:db8::/32",
		"10.0.0.0/8":      "10.0.0.0/8",
	}
	for in, want := range ok {
		got, err := normalizeRouteValue("ips", in)
		if err != nil {
			t.Errorf("ips %q: unexpected error %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ips %q: got %q, want %q", in, got, want)
		}
	}

	// The regression this file exists for: a pasted link must not reach
	// routing.json, because sing-box dies FATAL on it at every start.
	bad := []string{
		"https://boosty.to/ueyo",
		"boosty.to",
		"example.com",
		"1.2.3.4.5",
		"1.2.3.0/33",
		"not an ip",
		"",
		"   ",
	}
	for _, in := range bad {
		if got, err := normalizeRouteValue("ips", in); err == nil {
			t.Errorf("ips %q: accepted as %q, want rejection", in, got)
		}
	}
}

func TestNormalizeIPValueURLErrorPointsAtDomainsTab(t *testing.T) {
	_, err := normalizeRouteValue("ips", "https://boosty.to/ueyo")
	if err == nil {
		t.Fatal("expected an error")
	}
	if want := "домены"; !contains(err.Error(), want) {
		t.Errorf("error %q does not mention the %q tab", err, want)
	}
	if want := "boosty.to"; !contains(err.Error(), want) {
		t.Errorf("error %q does not name the extracted host %q", err, want)
	}
}

func TestNormalizeDomainValue(t *testing.T) {
	ok := map[string]string{
		"example.com":                  "example.com",
		"EXAMPLE.com":                  "example.com",
		" example.com ":                "example.com",
		"https://boosty.to/ueyo":       "boosty.to",
		"http://example.com":           "example.com",
		"example.com/some/path":        "example.com",
		"example.com:443":              "example.com",
		"*.example.com":                "example.com",
		".example.com":                 "example.com",
		"example.com.":                 "example.com",
		"https://sub.example.com/?q=1": "sub.example.com",
	}
	for in, want := range ok {
		got, err := normalizeRouteValue("domains", in)
		if err != nil {
			t.Errorf("domains %q: unexpected error %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("domains %q: got %q, want %q", in, got, want)
		}
	}

	bad := []string{
		"1.2.3.4",        // belongs in the ip list
		"192.168.0.0/16", // ditto
		"localhost",      // no dot, would match nothing useful
		"",
		"   ",
	}
	for _, in := range bad {
		if got, err := normalizeRouteValue("domains", in); err == nil {
			t.Errorf("domains %q: accepted as %q, want rejection", in, got)
		}
	}
}

func TestNormalizeRouteValueUnknownCategory(t *testing.T) {
	if _, err := normalizeRouteValue("nope", "x"); err == nil {
		t.Error("unknown category: want error")
	}
}

func TestSanitizeListDropsPoisonedLegacyEntries(t *testing.T) {
	// A routing.json written before validation existed: the URL in ips is
	// exactly what made sing-box restart-loop on a FATAL parse error.
	in := RouteList{
		Domains:   []string{"https://boosty.to/ueyo", "Example.COM", "1.2.3.4"},
		IPs:       []string{"https://boosty.to/ueyo", "1.2.3.4", "10.0.0.0/8"},
		Processes: []string{"Telegram"},
	}
	got := sanitizeList(in)

	wantDomains := []string{"boosty.to", "example.com"}
	if !equal(got.Domains, wantDomains) {
		t.Errorf("domains: got %v, want %v", got.Domains, wantDomains)
	}
	wantIPs := []string{"1.2.3.4/32", "10.0.0.0/8"}
	if !equal(got.IPs, wantIPs) {
		t.Errorf("ips: got %v, want %v", got.IPs, wantIPs)
	}
	if !equal(got.Processes, []string{"Telegram"}) {
		t.Errorf("processes: got %v", got.Processes)
	}
}

func TestSanitizeListNeverReturnsNil(t *testing.T) {
	got := sanitizeList(RouteList{})
	if got.Domains == nil || got.IPs == nil || got.Processes == nil {
		t.Fatalf("nil slice survived: %+v", got)
	}
}

func TestApplyRoutingEmitsOnlyParseableIPCIDR(t *testing.T) {
	// End to end: the poisoned value must not reach route.rules, because
	// that JSON is what sing-box refuses to start on.
	state := RoutingState{
		Enabled: true,
		Mode:    ModeProxy,
		Proxy: RouteList{
			IPs: []string{"https://boosty.to/ueyo", "10.0.0.0/8"},
		},
	}
	cfg := map[string]interface{}{}
	ApplyRouting(cfg, state, SelectorTag)

	route := cfg["route"].(map[string]interface{})
	rules := route["rules"].([]interface{})
	var seen []interface{}
	for _, r := range rules {
		m := r.(map[string]interface{})
		if v, ok := m["ip_cidr"]; ok {
			seen = v.([]interface{})
		}
	}
	if len(seen) != 1 || seen[0] != "10.0.0.0/8" {
		t.Fatalf("ip_cidr rule got %v, want [10.0.0.0/8]", seen)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
