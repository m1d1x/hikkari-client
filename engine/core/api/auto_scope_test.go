package api

import (
	"errors"
	"reflect"
	"testing"

	"NekkoVPN/core/engine"
	"NekkoVPN/core/profile"
)

func officialTestProfiles(t *testing.T, c *Core, names ...string) {
	t.Helper()
	f, err := c.profiles.Read()
	if err != nil {
		t.Fatal(err)
	}
	f.Subscriptions = append(f.Subscriptions, profile.Subscription{ID: "official", URL: "https://sub.hikkari.net/sub/test"})
	for i := range f.Profiles {
		for _, name := range names {
			if f.Profiles[i].Name == name {
				f.Profiles[i].Subscription = &profile.SubscriptionInfo{ID: "official"}
			}
		}
	}
	if err := c.profiles.Write(f); err != nil {
		t.Fatal(err)
	}
}

func TestAutoScopeAndManualSelectionAreTransactional(t *testing.T) {
	f := &alternateEngine{caps: engine.Capabilities{TUN: true, AutoSelect: true}}
	c := initAlternate(t, f)
	first, err := c.AddProfileFromURI("official")
	if err != nil {
		t.Fatal(err)
	}
	other, err := c.AddProfileFromURI("unrelated")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetAutoMode(true); err == nil {
		t.Fatal("standalone profiles enabled auto")
	}
	officialTestProfiles(t, c, first)
	if err := c.SetAutoMode(true); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.configuration.AutoProfiles, []string{first}) || !f.configuration.AutoSelect {
		t.Fatalf("wrong auto scope: %+v", f.configuration)
	}
	f.configureErr = errors.New("refused configuration")
	if err := c.SwitchProfile(other); err == nil {
		t.Fatal("expected application failure")
	}
	snapshot, err := c.profiles.Read()
	if err != nil || !snapshot.Auto || snapshot.Active == other {
		t.Fatal("failed switch changed persisted selection")
	}
	f.configureErr = nil
	if err := c.SwitchProfile(other); err != nil {
		t.Fatal(err)
	}
	snapshot, err = c.profiles.Read()
	if err != nil || snapshot.Auto || snapshot.Active != other || f.configuration.AutoSelect {
		t.Fatalf("manual click did not leave auto: %+v %v", snapshot, err)
	}
}
