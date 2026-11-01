package singbox

import "testing"

func TestExtendedHysteriaCannotSilentlyLoseItsPinOrMasks(t *testing.T) {
	for _, q := range []string{"pinSHA256=abcdef", "finalmask=W10"} {
		if _, err := ParseURI("hysteria2://password@localhost:443?" + q); err == nil {
			t.Fatal("unsupported Hysteria2 protection was discarded")
		}
	}
}

// The hub's Hysteria2 link for sing-box pins the leaf's public key; the pin
// reaches sing-box's TLS, and a malformed one refuses the link.
func TestTheHubsPublicKeyPinReachesSingBox(t *testing.T) {
	const spki = "cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd"
	d, err := ParseURI("hysteria2://auth@203.0.113.10:443?sni=mvtrgrvss.io&alpn=h3&obfs=salamander&obfs-password=pw&hikkari-spki=" + spki)
	if err != nil {
		t.Fatal(err)
	}
	tls := d.Config["tls"].(map[string]interface{})
	pins, _ := tls["certificate_public_key_sha256"].([]string)
	if len(pins) != 1 || pins[0] != "zc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc0=" || tls["insecure"] != nil {
		t.Fatalf("tls %v", tls)
	}
	for _, bad := range []string{"zz", "cdcd"} {
		if _, err := ParseURI("hysteria2://auth@203.0.113.10:443?obfs=salamander&obfs-password=pw&hikkari-spki=" + bad); err == nil {
			t.Fatalf("hikkari-spki=%s was accepted", bad)
		}
	}
}
