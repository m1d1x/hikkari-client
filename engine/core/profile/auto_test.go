package profile

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestAutoSourceDoesNotTrustProviderMetadataOrMixSubscriptions(t *testing.T) {
	f := &File{
		Active: "second",
		Subscriptions: []Subscription{
			{ID: "fake", URL: "https://hikkari.net.evil.test/sub/token"},
			{ID: "own", URL: "https://sub.hikkari.net/sub/token"},
			{ID: "other-own", URL: "https://sub.hikkari.net/sub/other"},
		},
		Profiles: []Profile{
			{Name: "fake", Subscription: &SubscriptionInfo{ID: "fake", Title: "hikkari", WebPageURL: "https://hikkari.net/pay"}},
			{Name: "first", Subscription: &SubscriptionInfo{ID: "own"}},
			{Name: "second", Subscription: &SubscriptionInfo{ID: "other-own"}},
			{Name: "manual"},
		},
	}
	if got := f.AutoProfiles(); !reflect.DeepEqual(got, []string{"second"}) {
		t.Fatalf("active source was not isolated: %v", got)
	}
	f.AutoSubscriptionID = "own"
	if got := f.AutoProfiles(); !reflect.DeepEqual(got, []string{"first"}) {
		t.Fatalf("pinned source was not retained: %v", got)
	}
	f.AutoSubscriptionID = "removed"
	if got := f.AutoProfiles(); len(got) != 0 {
		t.Fatalf("missing source fell through to another subscription: %v", got)
	}
	f.AutoSubscriptionID = "fake"
	if got := f.AutoProfiles(); len(got) != 0 {
		t.Fatalf("provider metadata authorized auto: %v", got)
	}
}

func TestLegacyAutoWithoutOurSourceBecomesManual(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "profiles.json"))
	if err := s.Write(&File{Auto: true, Active: "own-server", Profiles: []Profile{{Name: "own-server"}}}); err != nil {
		t.Fatal(err)
	}
	f, err := s.Read()
	if err != nil || f.Auto || f.Active != "own-server" {
		t.Fatalf("legacy global auto was not safely narrowed: %+v, %v", f, err)
	}
}
