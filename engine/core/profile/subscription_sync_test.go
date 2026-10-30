package profile

import (
	"path/filepath"
	"testing"
)

func subProfile(label, host, credential string) Profile {
	return Profile{Label: label, Connection: &Connection{URI: "vless://" + credential + "@" + host + ":443#" + label, Protocol: "vless", Host: host, Port: 443}}
}
func TestSubscriptionReconcilesRotationRemovalAndLocalNames(t *testing.T) {
	source := Subscription{ID: "provider", Info: SubscriptionInfo{Title: "VPN", TotalBytes: 100, ExpiresAt: 10}}
	file := &File{Profiles: []Profile{{Name: "unrelated", Label: "Manual", Connection: &Connection{URI: "vless://manual@other:443", Host: "other"}}}}
	names, err := ReconcileSubscription(file, &source, []Profile{subProfile("A", "a", "old"), subProfile("B", "b", "old")})
	if err != nil || len(names) != 2 {
		t.Fatalf("initial import: %v %v", names, err)
	}
	a, b := names[0], names[1]
	file.Active = b
	for i := range file.Profiles {
		if file.Profiles[i].Name == a {
			file.Profiles[i].Label = "My server"
		}
	}
	source.Info.TotalBytes = 200
	source.Info.ExpiresAt = 20
	names, err = ReconcileSubscription(file, &source, []Profile{subProfile("A", "new-a", "rotated"), subProfile("C", "c", "new")})
	if err != nil || len(names) != 2 || names[0] != a || file.Active != a {
		t.Fatalf("rotation lost ID/selection: %+v %v", file, err)
	}
	if len(file.Profiles) != 3 || file.Profiles[0].Name != "unrelated" {
		t.Fatal("another source was modified")
	}
	for _, p := range file.Profiles {
		if p.Name == b {
			t.Fatal("missing provider server survived")
		}
		if p.Name == a && (p.Label != "My server" || p.Connection.Host != "new-a" || p.Subscription.TotalBytes != 200 || p.Subscription.ExpiresAt != 20) {
			t.Fatalf("rotation/metadata/local label lost: %+v", p)
		}
	}
}
func TestSubscriptionProviderRenameKeepsID(t *testing.T) {
	source := Subscription{ID: "s"}
	file := &File{}
	names, err := ReconcileSubscription(file, &source, []Profile{subProfile("Old", "a", "key")})
	if err != nil {
		t.Fatal(err)
	}
	next, err := ReconcileSubscription(file, &source, []Profile{subProfile("New", "a", "key")})
	if err != nil || next[0] != names[0] || file.Profiles[0].Label != "New" {
		t.Fatal("provider rename lost ID or label")
	}
}
func TestDeletedServerStaysHiddenWithoutHidingSharedEndpointSibling(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "profiles.json"))
	source := Subscription{ID: "s"}
	file := &File{}
	drafts := []Profile{subProfile("A", "shared", "a"), subProfile("B", "shared", "b")}
	names, err := ReconcileSubscription(file, &source, drafts)
	if err != nil {
		t.Fatal(err)
	}
	file.Subscriptions = []Subscription{source}
	if err := store.Write(file); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(names[0]); err != nil {
		t.Fatal(err)
	}
	file, err = store.Read()
	if err != nil {
		t.Fatal(err)
	}
	source = file.Subscriptions[0]
	_, err = ReconcileSubscription(file, &source, []Profile{subProfile("A", "rotated", "new"), drafts[1]})
	if err != nil || len(file.Profiles) != 1 || file.Profiles[0].Name != names[1] {
		t.Fatalf("hidden server returned or hid sibling: %+v %v", file, err)
	}
}
func TestSubscriptionRejectsEmptySnapshotBeforeMutation(t *testing.T) {
	file := &File{Active: "existing", Profiles: []Profile{{Name: "existing"}}}
	_, err := ReconcileSubscription(file, &Subscription{ID: "s"}, nil)
	if err == nil || len(file.Profiles) != 1 || file.Active != "existing" {
		t.Fatal("empty response erased profiles")
	}
}
