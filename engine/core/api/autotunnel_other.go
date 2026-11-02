//go:build !windows && !darwin

package api

// publishAutoTunnel: no service owns the core on this platform — the engine
// runs it directly from the GUI's own config, which names the GUI-local rule
// set autotunnel.List keeps. Nothing to push.
func (c *Core) publishAutoTunnel(hosts []string) error { return nil }
