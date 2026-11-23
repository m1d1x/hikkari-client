package api

import (
	"context"
	"time"

	"NekkoVPN/core/subscription"
)

type countryHealth struct {
	down    string
	expires time.Time
}

func (c *Core) watchCountryHealth(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		if pf, err := c.profiles.Read(); err == nil {
			sources := map[string]bool{}
			for _, p := range pf.Profiles {
				if p.Country() != "" {
					sources[p.Subscription.ID] = true
				}
			}
			c.countryHealthMu.Lock()
			if c.countryHealth == nil {
				c.countryHealth = make(map[string]countryHealth)
			}
			for id := range c.countryHealth {
				if !sources[id] {
					delete(c.countryHealth, id)
				}
			}
			c.countryHealthMu.Unlock()
			for _, source := range pf.Subscriptions {
				if !sources[source.ID] || !subscription.IsHikkariURL(source.URL) {
					continue
				}
				if down, err := subscription.CountryDown(ctx, source.URL); err == nil {
					c.countryHealthMu.Lock()
					c.countryHealth[source.ID] = countryHealth{down, time.Now().Add(15 * time.Second)}
					c.countryHealthMu.Unlock()
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
