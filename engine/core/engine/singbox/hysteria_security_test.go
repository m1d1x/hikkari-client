package singbox

import (
	"encoding/json"
	"strings"
	"testing"

	"NekkoVPN/core/profile"
)

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

// The stored link is the exporter's output, so the pin has to survive it:
// without it sing-box rejects the hub's self-signed leaf and the profile never
// connects. Both shapes count: as parsed, and back from profiles.json.
func TestAHubPinSurvivesTheImportRoundTrip(t *testing.T) {
	const spki = "cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd"
	link := "hysteria2://auth@203.0.113.10:443?sni=mvtrgrvss.io&obfs=salamander&obfs-password=pw&hikkari-spki=" + spki
	profiles, err := (&Backend{}).Decode(link)
	if err != nil {
		t.Fatal(err)
	}
	if got := profiles[0].Connection.URI; !strings.Contains(got, "hikkari-spki="+spki) {
		t.Fatalf("stored link lost the pin: %s", got)
	}
	d, err := ParseURI(link)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(d.Config)
	if err != nil {
		t.Fatal(err)
	}
	var stored map[string]interface{}
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	out, err := ExportURI(profile.Profile{Label: "x", Kind: d.Kind, Config: stored})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hikkari-spki="+spki) {
		t.Fatalf("export from stored config lost the pin: %s", out)
	}
}
