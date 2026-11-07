package api

import (
	"context"
	"time"

	"NekkoVPN/core/engine"
	"NekkoVPN/core/uptime"
)

// routeWatchInterval is how often the core is asked which profile it holds. The
// question stays on the loopback, so this costs nothing on the network.
const routeWatchInterval = 5 * time.Second

// routeObserver is the engine's self-reporting view, nil if it has none.
func (c *Core) routeObserver() engine.RouteObserver {
	observer, _ := c.backend.(engine.RouteObserver)
	return observer
}

// carryingProfile is the profile traffic actually goes through. In auto mode that
// is the core's choice rather than the stored selection. "" while the engine is
// off. The answer comes from watchRoute, not from a call of its own: the UI asks
// for it on every poll.
func (c *Core) carryingProfile() string {
	c.routedMu.RLock()
	defer c.routedMu.RUnlock()
	return c.routed
}

// resolveCarryingProfile asks the engine directly.
func (c *Core) resolveCarryingProfile() string {
	if !c.backend.IsRunning() {
		return ""
	}
	if auto, err := c.profiles.AutoMode(); err == nil && auto {
		if observer := c.routeObserver(); observer != nil {
			if routed := observer.RoutedProfile(); routed != "" {
				return routed
			}
		}
	}
	return c.activeProfileProvider()
}

// watchRoute follows the profile in use. A move changes the exit, so the IP shown
// is re-read and the new profile gets its first sample straight away.
func (c *Core) watchRoute(ctx context.Context) {
	ticker := time.NewTicker(routeWatchInterval)
	defer ticker.Stop()
	for {
		current := c.resolveCarryingProfile()
		if current != "" {
			c.updateAutoPreference()
		}
		c.routedMu.Lock()
		previous := c.routed
		c.routed = current
		c.routedMu.Unlock()
		if current != previous {
			if previous != "" && current != "" {
				c.logger.Logf("[route] auto moved %q -> %q", previous, current)
			}
			c.health.RecheckEgress()
			c.uptime.Nudge()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// SetServerListOpen is the UI saying the server list went on or off screen.
// Opening it is also a person asking how things stand, so the IP is re-read.
func (c *Core) SetServerListOpen(open bool) {
	if open {
		c.health.RecheckEgress()
	}
	c.uptime.SetUIPresence(open)
}

// uptimeObserver adapts the engine's view for the tracker, nil if it has none.
func (c *Core) uptimeObserver() uptime.Observer {
	observer := c.routeObserver()
	if observer == nil {
		return nil
	}
	return uptimeObserverAdapter{observer}
}

type uptimeObserverAdapter struct{ engine.RouteObserver }

func (a uptimeObserverAdapter) Received(profileID string) (map[string]uint64, error) {
	return a.ReceivedBytes(profileID)
}

func (a uptimeObserverAdapter) OwnProbe(profileID string) (bool, int, bool) {
	return a.LastDelay(profileID)
}

func (c *Core) updateAutoPreference() {
	updater, ok := c.backend.(engine.AutoPreferenceUpdater)
	if !ok {
		return
	}
	snapshot, err := c.profiles.Read()
	if err != nil || !snapshot.Auto {
		return
	}
	// No external request: this only passes cached hub advice to the local core.
	if err := updater.SetAutoPreference(snapshot.AutoPreference(), snapshot.AutoPreferenceExpires()); err != nil {
		c.logger.Logf("[route] capacity advice not applied: %v", err)
	}
}
