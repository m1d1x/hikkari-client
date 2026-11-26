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

// resolveCarryingProfile is the profile traffic goes through, "" while the
// engine is off.
func (c *Core) resolveCarryingProfile() string {
	if !c.backend.IsRunning() {
		return ""
	}
	return c.activeProfileProvider()
}

// watchRoute follows the profile in use. A move changes the exit, so the IP shown
// is re-read and the new profile gets its first sample straight away.
func (c *Core) watchRoute(ctx context.Context) {
	ticker := time.NewTicker(routeWatchInterval)
	defer ticker.Stop()
	previous := ""
	for {
		current := c.resolveCarryingProfile()
		if current != previous {
			previous = current
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
