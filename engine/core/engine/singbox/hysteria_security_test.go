package singbox

import "testing"

func TestExtendedHysteriaCannotSilentlyLoseItsPinOrMasks(t *testing.T) {
	for _, q := range []string{"pinSHA256=abcdef", "finalmask=W10"} {
		if _, err := ParseURI("hysteria2://password@localhost:443?" + q); err == nil {
			t.Fatal("unsupported Hysteria2 protection was discarded")
		}
	}
}
