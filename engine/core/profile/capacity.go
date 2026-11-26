package profile

import (
	"time"

	"NekkoVPN/core/capacity"
	"NekkoVPN/core/subscription"
)

// CapacityFor is the fresh speed the profile's Hikkari subscription reported
// for it, nil when there is none.
func (f *File) CapacityFor(p Profile, now time.Time) *capacity.Metric {
	if p.Connection == nil || p.Subscription == nil {
		return nil
	}
	key := capacity.Key(p.Connection.URI)
	for _, source := range f.Subscriptions {
		if source.ID != p.Subscription.ID || !subscription.IsHikkariURL(source.URL) {
			continue
		}
		for _, m := range source.Capacity {
			if m.Key == key && m.Fresh(now) {
				value := m
				return &value
			}
		}
	}
	return nil
}
