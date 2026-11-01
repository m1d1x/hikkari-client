//go:build darwin

package macsvc

import (
	"reflect"
	"testing"
)

func TestParseServiceOrder(t *testing.T) {
	const out = `An asterisk (*) denotes that a network service is disabled.
(1) USB 10/100/1000 LAN
(Hardware Port: USB 10/100/1000 LAN, Device: en5)

(*) Thunderbolt Bridge
(Hardware Port: Thunderbolt Bridge, Device: bridge0)

(3) Wi-Fi
(Hardware Port: Wi-Fi, Device: en0)

(5) Happ
(Hardware Port: su.ffg.happ, Device: )
`
	want := []serviceDevice{{"USB 10/100/1000 LAN", "en5"}, {"Wi-Fi", "en0"}}
	if got := parseServiceOrder(out); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestParseForeignAnchors(t *testing.T) {
	got := parseForeignAnchors("  com.apple\n  com.apple/250.ApplicationFirewall\n  mullvad\n\n")
	if want := []string{"mullvad"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	// Empty, not null: the scan ran and found nothing foreign.
	if got := parseForeignAnchors("  com.apple\n"); got == nil || len(got) != 0 {
		t.Fatalf("got %#v", got)
	}
}
