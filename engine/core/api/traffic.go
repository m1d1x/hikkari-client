package api

import "NekkoVPN/core/engine"

// TrafficCounters reads local memory counters only; no external probe is made.
// Unsupported or stopped engines return nil, never a fabricated zero speed.
func (c *Core) TrafficCounters() (*engine.TrafficCounters, error) {
	observer, ok := c.backend.(engine.TrafficObserver)
	if !ok {
		return nil, nil
	}
	counters, err := observer.TrafficCounters()
	if err != nil {
		return nil, err
	}
	return &counters, nil
}
