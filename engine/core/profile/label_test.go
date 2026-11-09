package profile

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCleanLabel(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"🇫🇮 ФИНКА СТАРАЯ", "ФИНКА СТАРАЯ"},
		{"🇩🇪🇳🇱 Germany", "Germany"},
		{"Server 🇫🇮", "Server"},
		{"🏴\U000e0067\U000e0062\U000e0065\U000e006e\U000e0067\U000e007f UK", "UK"},
		{"🏳️‍🌈 Test 🚀", "Test 🚀"},
		{"🇫🇮", ""},
	} {
		if got := CleanLabel(tc.input); got != tc.want {
			t.Errorf("CleanLabel(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestImportedAndExistingLabels(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "profiles.json"))
	name, err := s.AddConnection(Profile{Label: "🇫🇮", Connection: &Connection{URI: "vless://example", Host: "example.com"}})
	if err != nil || name != "examplecom" {
		t.Fatalf("AddConnection: %q, %v", name, err)
	}
	pf, err := s.Read()
	if err != nil {
		t.Fatal(err)
	}
	pf.Profiles = append(pf.Profiles, Profile{Name: "old-id", Label: "🇩🇪 Германия"})
	pf.Active = "old-id"
	if err := s.Write(pf); err != nil {
		t.Fatal(err)
	}
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if list[0].Label != "example.com" || list[1].Label != "Германия" || list[1].Name != "old-id" || !list[1].Active {
		t.Fatalf("unexpected profiles: %+v", list)
	}
}

func TestProtocolLabelNamesTheWholeStack(t *testing.T) {
	for link, want := range map[string]string{
		"vless://id@h:443?security=reality&flow=xtls-rprx-vision&type=tcp": "VLESS · Reality · Vision",
		"vless://id@h:443?security=tls&type=xhttp&mode=stream-up":          "VLESS · XHTTP",
		"hysteria2://a@h:443?obfs=salamander":                              "Hysteria2 · Salamander",
		"hysteria2://a@h:443":                                              "Hysteria2",
	} {
		u := link[:strings.Index(link, ":")]
		if got := prettyProtocol(Profile{Connection: &Connection{URI: link, Protocol: u}}); got != want {
			t.Errorf("%s = %q, want %q", link, got, want)
		}
	}
	if got := prettyProtocol(Profile{Connection: &Connection{Protocol: "awg"}}); got != "AmneziaWG" {
		t.Errorf("awg = %q", got)
	}
}

func TestLabelCountryReadsTheExitFlagOfTheLink(t *testing.T) {
	link := func(fragment string) Profile {
		return Profile{Connection: &Connection{URI: "hikkari+v1://u:p@203.0.113.1:8443?sni=exit.example#" + fragment}}
	}
	for fragment, want := range map[string]string{
		"%F0%9F%87%B3%F0%9F%87%B1%20hikkari%20unl1-2%20через%20вход": "nl",
		"🇩🇪 hikkari fra1-1": "de",
		"hikkari unl1-2":    "",
		"":                  "",
	} {
		if got := LabelCountry(link(fragment)); got != want {
			t.Errorf("LabelCountry(#%s) = %q, want %q", fragment, got, want)
		}
	}
	if got := LabelCountry(Profile{}); got != "" {
		t.Errorf("a profile without a link: %q", got)
	}
}
