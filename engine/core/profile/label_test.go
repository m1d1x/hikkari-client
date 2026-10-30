package profile

import (
	"path/filepath"
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
