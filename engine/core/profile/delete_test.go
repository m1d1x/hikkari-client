package profile

import (
	"path/filepath"
	"testing"
)

func TestDeleteActiveAndLastProfiles(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "profiles.json"))
	first, err := s.Add("First", KindOutbound, map[string]interface{}{"type": "vless", "server": "first.example"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Add("Second", KindOutbound, map[string]interface{}{"type": "vless", "server": "second.example"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetActive(first); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAutoMode(true); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(first); err != nil {
		t.Fatal(err)
	}
	pf, err := s.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(pf.Profiles) != 1 || pf.Active != second {
		t.Fatalf("active profile did not fall back: %+v", pf)
	}
	if err := s.Delete(second); err != nil {
		t.Fatal(err)
	}
	pf, err = s.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(pf.Profiles) != 0 || pf.Active != "" || pf.Auto {
		t.Fatalf("last profile state not cleared: %+v", pf)
	}
	if err := s.Delete("missing"); err == nil {
		t.Fatal("missing profile must return an error")
	}
}
