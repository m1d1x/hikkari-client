// Typed Core methods. These are what gomobile bind exports as native methods
// on mobile, and what the JSON dispatcher in dispatch.go ultimately calls.
// Keep this file boring on purpose — pure delegation to the underlying
// managers. Any business logic belongs in core/* packages, not here.

package api

import (
	"NekkoVPN/core/clashapi"
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
// file stays tidy. Triggers a config rebuild so the deleted profile drops
// out of the selector group; restarts the engine if it was running (sing-
// box doesn't hot-remove outbounds either).
func (c *Core) DeleteProfile(name string) error {
	if err := c.profiles.Delete(name); err != nil {
		return err
	}
	c.uptime.Forget(name)
	c.uptime.Save()
	if err := c.rebuildConfig(); err != nil {
		return err
	}
	if vpn.ClashAPIRunning() {
		_ = c.vpn.Restart()
	}
	return nil
}

// SwitchProfile marks the named profile active and propagates the change:
//   - profiles.json: active = name
//   - config.json: selector default = profile tag (used on next start)
//   - running engine (if any): instant Clash-API select, no restart
func (c *Core) SwitchProfile(name string) error {
	if err := c.profiles.SetActive(name); err != nil {
		return err
	}
	if err := c.rebuildConfig(); err != nil {
		return err
	}
	if !vpn.ClashAPIRunning() {
		return nil
	}
	cl := clashapi.New("")
	return cl.SelectProxy(profile.SelectorTag, profile.ProfileTag(name))
}

// AddProfileFromURI parses a server URI and appends it as a new profile.
// The new profile is added to config.json's selector group so it can be
// probed for liveness immediately; if the engine is running it needs a
// restart to pick up the new outbound (sing-box doesn't hot-load).
func (c *Core) AddProfileFromURI(uri string) (string, error) {
	d, err := profile.ParseURI(uri)
	if err != nil {
		return "", err
	}
	name, err := c.profiles.Add(d.Label, d.Kind, d.Config)
	if err != nil {
		return "", err
	}
	if err := c.rebuildConfig(); err != nil {
		return name, err
	}
	if vpn.ClashAPIRunning() {
		_ = c.vpn.Restart()
	}
	return name, nil
}

// rebuildConfig snapshots all profiles and writes the canonical selector-
// shaped config.json. Call it after any mutation that adds, removes, or
// changes the active profile (or toggles auto-mode).
func (c *Core) rebuildConfig() error {
	specs, activeTag, autoMode, err := c.profiles.Snapshot()
	if err != nil {
		return err
	}
	cfg := make([]configfile.ProfileSpec, len(specs))
	for i, s := range specs {
		cfg[i] = configfile.ProfileSpec{Tag: s.Tag, Kind: s.Kind, Config: s.Config}
	}
	return c.configs.RebuildActive(cfg, activeTag, autoMode, profile.LegacyActiveTags)
}

// AutoMode reports whether auto-fastest selection is enabled.
func (c *Core) AutoMode() (bool, error) { return c.profiles.AutoMode() }

// SetAutoMode flips auto-fastest mode and propagates: writes the flag to
// profiles.json, rebuilds config.json (selector ± urltest, route.final
// adjusts), restarts the engine if running so sing-box picks it up.
func (c *Core) SetAutoMode(on bool) error {
	if err := c.profiles.SetAutoMode(on); err != nil {
		return err
	}
	if err := c.rebuildConfig(); err != nil {
		return err
	}
	if vpn.ClashAPIRunning() {
		_ = c.vpn.Restart()
	}
	return nil
}

// ============================================================
//  Uptime history
// ============================================================

func (c *Core) UptimeHistory() map[string][]PingSample { return c.uptime.Snapshot() }
