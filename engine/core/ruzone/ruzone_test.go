package ruzone

import (
	"net/netip"
	"slices"
	"testing"
)

func TestTheZonesAndEcosystemsAreListed(t *testing.T) {
	for _, d := range []string{"ru", "xn--p1ai", "su", "yandex.com", "vk.com", "gosuslugi.ru"} {
		if !slices.Contains(Domains(), d) {
			t.Errorf("Domains() has no %q", d)
		}
	}
	for _, d := range Domains() {
		if d == "" || d != canonical(d) {
			t.Errorf("domain %q is not a lowercase name without a leading dot", d)
		}
	}
}

func TestEveryCIDRParsesAndRussianAddressesAreCovered(t *testing.T) {
	var prefixes []netip.Prefix
	for _, c := range CIDRs() {
		p, err := netip.ParsePrefix(c)
		if err != nil {
			t.Fatalf("CIDR %q: %v", c, err)
		}
		prefixes = append(prefixes, p)
	}
	if len(prefixes) < 8000 {
		t.Fatalf("%d CIDRs, the edge table has more than 8000", len(prefixes))
	}
	// 77.88.55.242 is ya.ru, 87.240.132.67 is vk.com.
	for _, a := range []string{"77.88.55.242", "87.240.132.67", "2a02:6b8::2:242"} {
		addr := netip.MustParseAddr(a)
		if !slices.ContainsFunc(prefixes, func(p netip.Prefix) bool { return p.Contains(addr) }) {
			t.Errorf("%s is not covered", a)
		}
	}
}

func canonical(d string) string {
	b := []byte(d)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	if len(b) > 0 && b[0] == '.' {
		return ""
	}
	return string(b)
}
