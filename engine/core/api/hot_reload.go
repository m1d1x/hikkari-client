package api

import (
	"context"
	"time"

	"NekkoVPN/core/engine"
)

// reloadInPlace applies what the backend was last configured with to the
// running core without stopping it, so the tunnel and the connections on it
// survive a subscription sync or a profile edit (core patch 0009). It reports
// whether that happened; on false nothing was applied and the caller restarts
// the core as before. The line it logs pairs with the [lifecycle] begin/end of
// the operation around it.
func (c *Core) reloadInPlace(ctx context.Context, reason string) bool {
	reloader, ok := c.backend.(engine.HotReloader)
	if !ok || !c.backend.IsRunning() {
		return false
	}
	started := time.Now()
	outcome, err := reloader.Reload(ctx)
	took := time.Since(started).Round(time.Millisecond)
	if c.logger != nil {
		switch {
		case err != nil:
			c.logger.Logf("[lifecycle] hot reason=%s applied=false took=%s err=%q", reason, took, subscriptionSecret.ReplaceAllString(err.Error(), "[redacted-link]"))
		case outcome.Applied:
			c.logger.Logf("[lifecycle] hot reason=%s applied=true took=%s added=%d removed=%d replaced=%d", reason, took, outcome.Added, outcome.Removed, outcome.Replaced)
		default:
			c.logger.Logf("[lifecycle] hot reason=%s applied=false took=%s declined=%q", reason, took, outcome.Declined)
		}
	}
	return err == nil && outcome.Applied
}
