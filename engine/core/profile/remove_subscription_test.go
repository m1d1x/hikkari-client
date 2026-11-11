package profile

import "testing"

func TestRemoveSubscriptionTakesItsProfilesAndAutoWithIt(t *testing.T) {
	hub := "https://sub.hikkari.net/sub/token"
	pf := &File{
		Active: "h1", Auto: true, AutoSubscriptionID: "a",
		Subscriptions: []Subscription{{ID: "a", URL: hub}, {ID: "b", URL: "https://provider.test/sub"}},
		Profiles: []Profile{
			{Name: "h1", Subscription: &SubscriptionInfo{ID: "a"}},
			{Name: "h2", Subscription: &SubscriptionInfo{ID: "a"}},
			{Name: "o1", Subscription: &SubscriptionInfo{ID: "b"}},
			{Name: "own"},
		},
	}
	if got := RemoveSubscription(pf, "missing"); got != nil || len(pf.Profiles) != 4 {
		t.Fatalf("an unknown source removed %v", got)
	}
	got := RemoveSubscription(pf, "a")
	if len(got) != 2 || got[0] != "h1" || got[1] != "h2" {
		t.Fatalf("removed %v", got)
	}
	if len(pf.Subscriptions) != 1 || pf.Subscriptions[0].ID != "b" {
		t.Fatalf("sources left %+v", pf.Subscriptions)
	}
	if len(pf.Profiles) != 2 || pf.Active != "o1" {
		t.Fatalf("profiles left %+v, active %q", pf.Profiles, pf.Active)
	}
	if pf.Auto || pf.AutoSubscriptionID != "" {
		t.Fatalf("auto mode outlived its only source: %v %q", pf.Auto, pf.AutoSubscriptionID)
	}
	RemoveSubscription(pf, "b")
	if len(pf.Profiles) != 1 || pf.Active != "own" {
		t.Fatalf("a profile of no source went or the selection did not move: %+v %q", pf.Profiles, pf.Active)
	}
}
