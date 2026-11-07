//go:build !windows && !darwin

package api

// stopForeignVPNInService: no service runs with the rights to end another client's
// tunnel on this platform.
func (c *Core) stopForeignVPNInService() []string { return nil }
