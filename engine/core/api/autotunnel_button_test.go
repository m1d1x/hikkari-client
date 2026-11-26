package api

import (
	"errors"
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

// The "авто" button reaches the adapter as the auto tunnel and nothing else:
// any profile works with it, a refused configuration keeps the button as it
// was, and picking a profile by hand leaves it alone.
func TestTheAutoButtonIsTheAutoTunnelAndTransactional(t *testing.T) {
	f := &alternateEngine{caps: engine.Capabilities{TUN: true, AutoTunnel: true}}
	c := initAlternate(t, f)
	if _, err := c.AddProfileFromURI("standalone"); err != nil {
		t.Fatal(err)
	}
	other, err := c.AddProfileFromURI("another")
	if err != nil {
		t.Fatal(err)
	}
	f.configureErr = errors.New("refused configuration")
	if err := c.SetAutoTunnel(true); err == nil {
		t.Fatal("expected application failure")
	}
	if on, err := c.AutoTunnel(); err != nil || on {
		t.Fatalf("a refused configuration turned the button on: %v %v", on, err)
	}
	f.configureErr = nil
	if err := c.SetAutoTunnel(true); err != nil {
		t.Fatal(err)
	}
	if !f.configuration.AutoTunnel || len(f.configuration.Profiles) != 2 {
		t.Fatalf("adapter got %+v", f.configuration)
	}
	if err := c.SwitchProfile(other); err != nil {
		t.Fatal(err)
	}
	if on, err := c.AutoTunnel(); err != nil || !on {
		t.Fatalf("a manual pick turned the button off: %v %v", on, err)
	}
	if err := c.SetAutoTunnel(false); err != nil {
		t.Fatal(err)
	}
	if f.configuration.AutoTunnel {
		t.Fatal("the adapter still sends silent sites through the tunnel")
	}
}
