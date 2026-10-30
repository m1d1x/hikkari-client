package netinfo

import (
	"bytes"
	"compress/gzip"
	"net"
	"testing"
)

func gz(t *testing.T, s string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	if _, err := w.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
	w.Close()
	return b.Bytes()
}

func TestLookupIn(t *testing.T) {
	// 5.0.0.0-5.0.0.255 → AS12389 / moscow, 5.0.2.0-5.0.2.255 → AS8359 only.
	asn := parse(gz(t, "83886592,83886847,8359\n83886080,83886335,12389\nbroken,row\n"))
	area := parse(gz(t, "83886080,83886335,moscow\n"))

	cases := []struct {
		ip   string
		want Info
	}{
		{"5.0.0.10", Info{ASN: 12389, Area: "moscow"}},
		{"5.0.2.1", Info{ASN: 8359}},
		{"5.0.1.1", Info{}}, // gap between ranges
		{"1.1.1.1", Info{}}, // before every range
		{"2001:db8::1", Info{}},
	}
	for _, c := range cases {
		if got := lookupIn(asn, area, net.ParseIP(c.ip)); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.ip, got, c.want)
		}
	}
}

func TestEmptyEmbeddedDB(t *testing.T) {
	// The shipped placeholders are empty gzip streams: lookups must just
	// report nothing, never panic.
	if Available() {
		t.Skip("database generated into this build")
	}
	if got := Lookup(net.ParseIP("5.0.0.10")); got != (Info{}) {
		t.Fatalf("empty database resolved %+v", got)
	}
}
