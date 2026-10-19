// Typed Core methods. These are what gomobile bind exports as native methods
// on mobile, and what the JSON dispatcher in dispatch.go ultimately calls.
// Keep this file boring on purpose — pure delegation to the underlying
// managers. Any business logic belongs in core/* packages, not here.

package api

import (
	"fmt"
	"sync"

	"NekkoVPN/core/applist"
	"NekkoVPN/core/cleanliness"
	"NekkoVPN/core/clashapi"
	"NekkoVPN/core/configfile"
	"NekkoVPN/core/geoip"
	"NekkoVPN/core/health"
	"NekkoVPN/core/profile"
	"NekkoVPN/core/uptime"
	"NekkoVPN/core/vpn"
)

// Re-exported types — same Go type, shorter name inside api/. JSON shape is
// unchanged, so every UI (Flutter, gomobile) sees the same fields.
type (
	HealthInfo   = health.Info
	RoutingState = configfile.RoutingState
	RoutingMode  = configfile.RoutingMode
	ProfileInfo  = profile.Info
	PingSample   = uptime.Sample

	CleanlinessReport = cleanliness.Report
)

// ============================================================
//  VPN control
// ============================================================

// StartVPN re-applies config healing first: config.json is otherwise only
// rewritten on profile/routing changes, so fixes shipped in a new build
// (tun options, dns, log level) would never reach an existing install.
func (c *Core) StartVPN() error {
	if err := c.rebuildConfig(); err != nil {
		c.logger.Logf("vpn.Start: config rebuild failed (starting with existing config): %v", err)
	}
	if err := c.vpn.Start(); err != nil {
		return err
	}
	c.diag.AfterStart(c.ctx)
	return nil
}

func (c *Core) StopVPN() error {
	c.diag.Stop()
	return c.vpn.Stop()
}

func (c *Core) RestartVPN() error {
	c.diag.Stop()
	if err := c.rebuildConfig(); err != nil {
		c.logger.Logf("vpn.Restart: config rebuild failed (restarting with existing config): %v", err)
	}
	if err := c.vpn.Restart(); err != nil {
		return err
	}
	c.diag.AfterStart(c.ctx)
	return nil
}

// IsRunning asks the sing-box Clash API whether the core is answering.
// It does NOT tell you the tunnel is actually carrying traffic — use Health
// for that.
func (c *Core) IsRunning() bool { return vpn.ClashAPIRunning() }

// ============================================================
//  Health & egress
// ============================================================

func (c *Core) Health() HealthInfo { return c.health.Probe() }
func (c *Core) ExternalIP() string { return c.health.ExternalIP() }

// ServerCleanliness returns the cached "whiteness" report for the active
// egress: exit IP, ASN, hosting flag, optional fraud-score, and how
// Cloudflare classifies it. Instant read — the probing runs in background.
func (c *Core) ServerCleanliness() CleanlinessReport { return c.cleanliness.Report() }

// ============================================================
//  Logging
// ============================================================

func (c *Core) LogTail() string { return c.logger.Tail() }

// ============================================================
//  Routing (split-tunnel: master toggle + mode + per-mode lists)
// ============================================================

func (c *Core) Routing() (RoutingState, error) { return c.configs.ReadRouting() }

func (c *Core) SetRoutingEnabled(on bool) error {
	return c.configs.SetRoutingEnabled(on)
}

func (c *Core) SetRoutingMode(mode string) error {
	return c.configs.SetRoutingMode(RoutingMode(mode))
}

func (c *Core) AddRule(mode, category, value string) error {
	return c.configs.AddRouteRule(RoutingMode(mode), category, value)
}

func (c *Core) RemoveRule(mode, category, value string) error {
	return c.configs.RemoveRouteRule(RoutingMode(mode), category, value)
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

// AddProfileFromURI parses a server URI or subscription URL and appends
// the resulting profile(s). The new profile(s) are added to config.json's
// selector group so they can be probed for liveness immediately.
func (c *Core) AddProfileFromURI(uri string) (string, error) {
	drafts, err := profile.ParseURIs(uri)
	if err != nil {
		return "", err
	}
	if len(drafts) == 0 {
		return "", fmt.Errorf("профили не найдены в ссылке")
	}
	var firstName string
	for _, d := range drafts {
		name, err := c.profiles.Add(d.Label, d.Kind, d.Config)
		if err != nil {
			return "", err
		}
		if firstName == "" {
			firstName = name
		}
	}
	if err := c.rebuildConfig(); err != nil {
		return firstName, err
	}
	if vpn.ClashAPIRunning() {
		_ = c.vpn.Restart()
	}
	return firstName, nil
}

// ExportProfileURI serialises a stored profile back into a shareable import
// link (the inverse of AddProfileFromURI) for the share sheet's QR + copy.
func (c *Core) ExportProfileURI(name string) (string, error) {
	p, ok, err := c.profiles.Get(name)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("профиль %q не найден", name)
	}
	return profile.ExportURI(p)
}

// ProfileCountries resolves the lowercase ISO 3166-1 alpha-2 country code
// for each profile's server host, keyed by profile name. A resolved host is
// cached forever (in-process and on disk — see geoip.Load, called from
// InitWithVPN) since its country can't change without its IP changing,
// i.e. without the profile's endpoint itself changing — so repeated calls
// after the first are free. Profiles whose host can't be determined or
// resolved map to "" and are retried on the next call.
//
// Resolutions run concurrently across profiles — this is a synchronous FFI
// call from the UI thread, so a cold cache (DNS + online fallback per host,
// up to ~3.5s each) must not serialize into N*3.5s.
func (c *Core) ProfileCountries() (map[string]string, error) {
	pf, err := c.profiles.Read()
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(pf.Profiles))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, p := range pf.Profiles {
		wg.Add(1)
		go func(p profile.Profile) {
			defer wg.Done()
			code := geoip.Resolve(profile.PeerHost(p))
			mu.Lock()
			out[p.Name] = code
			mu.Unlock()
		}(p)
	}
	wg.Wait()
	geoip.Save()
	return out, nil
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

// ============================================================
//  Service uptime (on-demand pings of arbitrary user services
//  through the active profile)
// ============================================================

// ServicePingTarget is a single user service to probe through the active
// profile.
type ServicePingTarget struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// ServicePingResult is the outcome of probing one ServicePingTarget.
type ServicePingResult struct {
	Name  string `json:"name"`
	OK    bool   `json:"ok"`
	RttMs int    `json:"rtt_ms"`
}

// PingServices probes each service in parallel through the active profile's
// Clash-API delay endpoint, records every result in the service uptime
// tracker (keyed by "<activeTag>|<service name>") and returns the results.
// This is an on-demand, one-shot call — no background loop, no staggering.
func (c *Core) PingServices(activeTag string, services []ServicePingTarget) []ServicePingResult {
	results := make([]ServicePingResult, len(services))
	cl := clashapi.New("")

	var wg sync.WaitGroup
	for i, svc := range services {
		wg.Add(1)
		go func(i int, svc ServicePingTarget) {
			defer wg.Done()
			ms, err := cl.Delay(activeTag, svc.URL, uptime.PingTimeoutMs)
			res := ServicePingResult{Name: svc.Name}
			if err == nil {
				res.OK = true
				res.RttMs = ms
			}
			results[i] = res
		}(i, svc)
	}
	wg.Wait()

	for i, svc := range services {
		c.serviceUptime.Record(activeTag+"|"+svc.Name, results[i].OK, results[i].RttMs)
	}
	c.serviceUptime.Save()

	return results
}

// ServiceUptimeHistory returns the rolling history of on-demand service pings,
// keyed by "<activeTag>|<service name>".
func (c *Core) ServiceUptimeHistory() map[string][]PingSample { return c.serviceUptime.Snapshot() }

// ============================================================
//  Custom services (user-added ping targets)
// ============================================================

func (c *Core) ListCustomServices() []uptime.ServiceTarget {
	c.customServicesMu.RLock()
	defer c.customServicesMu.RUnlock()
	out := make([]uptime.ServiceTarget, len(c.customServices))
	copy(out, c.customServices)
	return out
}

func (c *Core) AddCustomService(name, url string) error {
	c.customServicesMu.Lock()
	defer c.customServicesMu.Unlock()
	for _, s := range c.customServices {
		if s.Name == name {
			return fmt.Errorf("service %q already exists", name)
		}
	}
	c.customServices = append(c.customServices, uptime.ServiceTarget{Name: name, URL: url})
	return c.saveCustomServices()
}

// ============================================================
//  App discovery (process picker for routing rules)
// ============================================================

func (c *Core) ListApps(all bool) []applist.AppEntry { return applist.List(all) }

func (c *Core) RemoveCustomService(name string) error {
	c.customServicesMu.Lock()
	defer c.customServicesMu.Unlock()
	for i, s := range c.customServices {
		if s.Name == name {
			c.customServices = append(c.customServices[:i], c.customServices[i+1:]...)
			return c.saveCustomServices()
		}
	}
	return fmt.Errorf("service %q not found", name)
}
