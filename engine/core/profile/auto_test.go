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

func TestServerManagedAutoKeepsExplicitCountriesOutOfThePool(t *testing.T) {
	const base = "hikkari+v1://hikkari:test-key@192.0.2.1:8443?sni=relay.example&egress="
	source := &SubscriptionInfo{ID: "own"}
	f := &File{
		Active:        "nl",
		Subscriptions: []Subscription{{ID: source.ID, URL: "https://sub.hikkari.net/sub/token"}},
		Profiles: []Profile{
			{Name: "nl", Subscription: source, Connection: &Connection{URI: base + "nl"}},
			{Name: "de", Subscription: source, Connection: &Connection{URI: base + "de"}},
			{Name: "auto", Subscription: source, Connection: &Connection{URI: base + "auto"}},
			{Name: "local", Connection: &Connection{URI: base + "auto"}},
		},
	}
	if ids := f.AutoProfiles(); !reflect.DeepEqual(ids, []string{"auto"}) {
		t.Fatalf("auto candidates: %v", ids)
	}
	if f.Active != "nl" {
		t.Fatalf("manual NL was changed to %q", f.Active)
	}
	f.Profiles[2] = f.Profiles[3]
	if ids := f.AutoProfiles(); len(ids) != 0 {
		t.Fatalf("explicit countries entered Auto after removal of its profile: %v", ids)
	}
}
