package geoip

import (
	"net"
	"testing"
)

func TestLookupIP_KnownRange(t *testing.T) {
	// 46.172.224.0/19 is in data/ranges.csv.gz tagged "ad" (Andorra).
	cases := []struct {
		ip   string
		code string
		ok   bool
	}{
		{"46.172.224.0", "ad", true},   // range start
		{"46.172.255.255", "ad", true}, // range end
		{"46.172.230.5", "ad", true},   // range middle
		{"0.0.0.1", "", false},         // unallocated space, no country range
	}
	for _, c := range cases {
		code, ok := LookupIP(net.ParseIP(c.ip))
		if ok != c.ok || (ok && code != c.code) {
			t.Errorf("LookupIP(%s) = (%q, %v), want (%q, %v)", c.ip, code, ok, c.code, c.ok)
		}
	}
}

func TestLookupIP_IPv6Unsupported(t *testing.T) {
	if _, ok := LookupIP(net.ParseIP("2001:db8::1")); ok {
		t.Error("LookupIP should reject IPv6 in v1")
	}
}

func TestResolve_DirectIPLocalHit(t *testing.T) {
	if code := Resolve("46.172.230.5"); code != "ad" {
		t.Errorf("Resolve(direct IP) = %q, want \"ad\"", code)
	}
	// second call must hit the cache, not re-run the lookup — same result.
	if code := Resolve("46.172.230.5"); code != "ad" {
		t.Errorf("Resolve(cached) = %q, want \"ad\"", code)
	}
}

func TestResolve_Empty(t *testing.T) {
	if code := Resolve(""); code != "" {
		t.Errorf("Resolve(\"\") = %q, want \"\"", code)
	}
}
