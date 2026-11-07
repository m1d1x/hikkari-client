package profile

import (
	"NekkoVPN/core/capacity"
	"NekkoVPN/core/subscription"
	"sort"
	"time"
)

// AutoSource resolves one authoritative provider source, never a display title
// or a provider-supplied website. Legacy groups without a source URL cannot opt in.
func (f *File) AutoSource() string {
	eligible := map[string]bool{}
	for _, s := range f.Subscriptions {
		if subscription.IsHikkariURL(s.URL) {
			eligible[s.ID] = true
		}
	}
	if f.AutoSubscriptionID != "" {
		if eligible[f.AutoSubscriptionID] {
			return f.AutoSubscriptionID
		}
		return ""
	}
	for _, p := range f.Profiles {
		if p.Name == f.Active && p.Subscription != nil && eligible[p.Subscription.ID] {
			return p.Subscription.ID
		}
	}
	for _, p := range f.Profiles {
		if p.Subscription != nil && eligible[p.Subscription.ID] {
			return p.Subscription.ID
		}
	}
	return ""
}

func (f *File) AutoProfiles() []string {
	ids := []string{}
	source := f.AutoSource()
	if source == "" {
		return ids
	}
	for _, p := range f.Profiles {
		if p.Subscription != nil && p.Subscription.ID == source {
			ids = append(ids, p.Name)
		}
	}
	return ids
}

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
func (f *File) AutoPreference() []string {
	eligible := map[string]bool{}
	for _, id := range f.AutoProfiles() {
		eligible[id] = true
	}
	type score struct {
		id   string
		mbps float64
	}
	scores := []score{}
	for _, p := range f.Profiles {
		if eligible[p.Name] {
			if m := f.CapacityFor(p, time.Now()); m != nil {
				scores = append(scores, score{p.Name, m.Mbps})
			}
		}
	}
	sort.SliceStable(scores, func(i, j int) bool { return scores[i].mbps > scores[j].mbps })
	out := []string{}
	for _, s := range scores {
		out = append(out, s.id)
	}
	return out
}

func (f *File) AutoPreferenceExpires() int64 {
	var earliest int64
	for _, p := range f.Profiles {
		if m := f.CapacityFor(p, time.Now()); m != nil {
			if earliest == 0 || m.ExpiresAt < earliest {
				earliest = m.ExpiresAt
			}
		}
	}
	return earliest
}
