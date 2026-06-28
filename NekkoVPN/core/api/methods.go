// Typed Core methods. These are what gomobile bind exports as native methods
// on mobile, and what the JSON dispatcher in dispatch.go ultimately calls.
// Keep this file boring on purpose — pure delegation to the underlying
// managers. Any business logic belongs in core/* packages, not here.

package api

import (
	"NekkoVPN/core/configfile"
	"NekkoVPN/core/health"
	"NekkoVPN/core/profile"
	"NekkoVPN/core/uptime"
	"NekkoVPN/core/vpn"
)

// Re-exported types — same Go type, shorter name inside api/. JSON shape is
// unchanged, so every UI (Wails, Flutter, gomobile) sees the same fields.
type (
	HealthInfo  = health.Info
	Whitelist   = configfile.Whitelist
	ProfileInfo = profile.Info
	PingSample  = uptime.Sample
)

// ============================================================
//  VPN control
// ============================================================

func (c *Core) StartVPN() error   { return c.vpn.Start() }
func (c *Core) StopVPN() error    { return c.vpn.Stop() }
func (c *Core) RestartVPN() error { return c.vpn.Restart() }

// IsRunning asks the sing-box Clash API whether the core is answering.
// It does NOT tell you the tunnel is actually carrying traffic — use Health
// for that.
func (c *Core) IsRunning() bool { return vpn.ClashAPIRunning() }

// ============================================================
//  Health & egress
// ============================================================

func (c *Core) Health() HealthInfo { return c.health.Probe() }
func (c *Core) ExternalIP() string { return c.health.ExternalIP() }

// ============================================================
//  Logging
// ============================================================

func (c *Core) LogTail() string { return c.logger.Tail() }

// ============================================================
//  Whitelist (bypass-VPN route rules)
// ============================================================

func (c *Core) Whitelist() (Whitelist, error)        { return c.configs.GetWhitelist() }
func (c *Core) AddRule(category, value string) error { return c.configs.AddRule(category, value) }
func (c *Core) RemoveRule(category, value string) error {
	return c.configs.RemoveRule(category, value)
}

// ============================================================
//  Profiles
// ============================================================

func (c *Core) Profiles() ([]ProfileInfo, error)       { return c.profiles.List() }
func (c *Core) ActiveProfile() (string, error)         { return c.profiles.Active() }
func (c *Core) RenameProfile(name, label string) error { return c.profiles.Rename(name, label) }

// DeleteProfile removes a profile and also drops its uptime history so the
// file stays tidy.
func (c *Core) DeleteProfile(name string) error {
	if err := c.profiles.Delete(name); err != nil {
		return err
	}
	c.uptime.Forget(name)
	c.uptime.Save()
	return nil
}

// SwitchProfile swaps only the endpoint block in config.json to the chosen
// server, marks it active, and (if the core is up) restarts to apply.
func (c *Core) SwitchProfile(name string) error {
	endpoints, err := c.profiles.PrepareSwitch(name)
	if err != nil {
		return err
	}
	if err := c.configs.SetEndpoints(endpoints); err != nil {
		return err
	}
	if err := c.profiles.SetActive(name); err != nil {
		return err
	}
	if vpn.ClashAPIRunning() {
		return c.vpn.Restart()
	}
	return nil
}

// ============================================================
//  Uptime history
// ============================================================

func (c *Core) UptimeHistory() map[string][]PingSample { return c.uptime.Snapshot() }
