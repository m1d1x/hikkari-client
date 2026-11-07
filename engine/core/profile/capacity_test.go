package profile

import (
	"NekkoVPN/core/capacity"
	"reflect"
	"testing"
	"time"
)

func TestCapacityRanksOnlyOwnMatchingFreshProfiles(t *testing.T) {
	now := time.Now()
	p := Profile{Name: "slow", Connection: &Connection{URI: "vless://a@slow.test:443?fp=chrome"}, Subscription: &SubscriptionInfo{ID: "ours"}}
	fast := p
	fast.Name = "fast"
	fast.Connection = &Connection{URI: "vless://b@fast.test:443?fp=chrome"}
	metric := func(p Profile, speed float64) capacity.Metric {
		return capacity.Metric{Key: capacity.Key(p.Connection.URI), Mbps: speed, Points: 1, MeasuredAt: now.Unix(), ExpiresAt: now.Add(time.Hour).Unix()}
	}
	f := File{Profiles: []Profile{p, fast}, Subscriptions: []Subscription{{ID: "ours", URL: "https://sub.hikkari.net/sub/example", Capacity: []capacity.Metric{metric(p, 10), metric(fast, 100)}}}}
	if got := f.AutoPreference(); !reflect.DeepEqual(got, []string{"fast", "slow"}) {
		t.Fatalf("ranking: %v", got)
	}
	fast.Connection = &Connection{URI: "vless://b@fast.test:443?fp=firefox"}
	f.Profiles[1] = fast
	if f.CapacityFor(fast, now) != nil {
		t.Fatal("changed profile reused old measurement")
	}
	if f.CapacityFor(p, now.Add(2*time.Hour)) != nil {
		t.Fatal("expired measurement survived")
	}
	f.Subscriptions[0].URL = "https://other.test/sub/example"
	if len(f.AutoPreference()) != 0 || f.CapacityFor(p, now) != nil {
		t.Fatal("third-party subscription supplied priority")
	}
}
