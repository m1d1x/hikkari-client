// Typed Core methods. These are what gomobile bind exports as native methods
// on mobile, and what the JSON dispatcher in dispatch.go ultimately calls.
// Keep this file boring on purpose — pure delegation to the underlying
// managers. Any business logic belongs in core/* packages, not here.

package api

import (
	"fmt"
	"sync"
	"time"

	"NekkoVPN/core/applist"
	"NekkoVPN/core/cleanliness"
	"NekkoVPN/core/downdetector"
	"NekkoVPN/core/engine"
	"NekkoVPN/core/geoip"
	"NekkoVPN/core/health"
	"NekkoVPN/core/profile"
	"NekkoVPN/core/routing"
	"NekkoVPN/core/subscription"
	"NekkoVPN/core/uptime"
	"net/url"
)

// Re-exported types — same Go type, shorter name inside api/. JSON shape is
// unchanged, so every UI (Flutter, gomobile) sees the same fields.
type (
	HealthInfo   = health.Info
	RoutingState = routing.RoutingState
	RoutingMode  = routing.RoutingMode
	ProfileInfo  = profile.Info
	PingSample   = uptime.Sample

	CleanlinessReport = cleanliness.Report
)

// ============================================================
//  VPN control
// ============================================================

func (c *Core) EngineInfo() engine.Info { return c.backend.Info() }
func (c *Core) ValidateConfiguration() error {
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	policy, err := c.manualRouting()
	if err != nil {
		return err
	}
	pf, err := c.profiles.Read()
	if err != nil {
		return err
	}
	effective, err := c.effectiveRouting(policy)
	if err != nil {
		return err
	}
	return engine.ValidateConfiguration(engine.Configuration{
		Profiles: pf.Profiles, ActiveProfile: pf.Active, AutoSelect: pf.Auto, Routing: effective,
	}, c.backend.Info().Capabilities)
}
func (c *Core) StartVPN() error {
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	if err := c.flushRoutingLocked(); err != nil {
		return err
	}
	started := time.Now()
	err := c.startVPN()
	if err != nil {
		c.SubscriptionWake()
	}
	if !c.routingPreview {
		c.reportConnect(started, err)
	}
	return err
}

func (c *Core) startVPN() error {
	if c.routingPreview {
		return fmt.Errorf("режим предпросмотра не запускает VPN")
	}
	if err := c.requireProfiles(); err != nil {
		return err
	}
	if err := c.rebuildConfig(); err != nil {
		return err
	}
	if !c.backend.Info().Capabilities.TUN {
		return fmt.Errorf("выбранный адаптер не поддерживает TUN на этой ОС; требуется реализация TUN для выбранного адаптера")
	}
	return c.backend.Start(c.ctx)
}
func (c *Core) StopVPN() error {
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	if !c.routingPreview {
		if err := c.backend.Stop(); err != nil {
			return err
		}
	}
	return c.flushRoutingLocked()
}
func (c *Core) RestartVPN() error {
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	wasRunning := c.backend.IsRunning()
	pendingRestart := c.routingApp.pending != nil && wasRunning && !routing.ActiveEqual(c.routingApp.pending.effective, c.RoutingStatus().Effective)
	if err := c.flushRoutingLocked(); err != nil {
		return err
	}
	if pendingRestart {
		return nil
	}
	return c.restartVPN()
}

func (c *Core) restartVPN() error {
	if c.routingPreview {
		return fmt.Errorf("режим предпросмотра не перезапускает VPN")
	}
	if err := c.requireProfiles(); err != nil {
		return err
	}
	if err := c.backend.Stop(); err != nil {
		return err
	}
	return c.startVPN()
}
func (c *Core) IsRunning() bool { return c.backend.IsRunning() }

func (c *Core) requireProfiles() error {
	pf, err := c.profiles.Read()
	if err != nil {
		return err
	}
	if len(pf.Profiles) == 0 {
		return fmt.Errorf("сначала добавьте сервер или подписку")
	}
	return nil
}

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

func (c *Core) Routing() (RoutingState, error) { return c.RoutingStatus().Manual, nil }

func (c *Core) SetRoutingEnabled(on bool) error {
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	policy, err := c.manualRouting()
	if err != nil {
		return err
	}
	policy.Enabled = on
	effective, err := c.effectiveRouting(policy)
	if err != nil {
		return err
	}
	if err := engine.ValidateConfiguration(engine.Configuration{Routing: effective}, c.backend.Info().Capabilities); err != nil {
		return err
	}
	return c.changeManualRouting(func(state *routing.RoutingState) error { state.Enabled = on; return nil })
}

func (c *Core) SetRoutingMode(mode string) error {
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	policy, err := c.manualRouting()
	if err != nil {
		return err
	}
	policy.Mode = RoutingMode(mode)
	effective, err := c.effectiveRouting(policy)
	if err != nil {
		return err
	}
	if err := engine.ValidateConfiguration(engine.Configuration{Routing: effective}, c.backend.Info().Capabilities); err != nil {
		return err
	}
	return c.changeManualRouting(func(state *routing.RoutingState) error {
		if mode != "bypass" && mode != "proxy" {
			return fmt.Errorf("неизвестный режим %q", mode)
		}
		state.Mode = RoutingMode(mode)
		return nil
	})
}

func (c *Core) AddRule(mode, category, value string) error {
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	caps := c.backend.Info().Capabilities
	if category == "domains" && !caps.DomainRouting {
		return fmt.Errorf("выбранный адаптер не поддерживает правила по доменам")
	}
	if category == "ips" && !caps.IPRouting {
		return fmt.Errorf("выбранный адаптер не поддерживает правила по IP")
	}
	if category == "processes" && !caps.ProcessRouting {
		return fmt.Errorf("движок не поддерживает маршрутизацию приложений")
	}
	if category == "folders" && !caps.FolderRouting {
		return fmt.Errorf("движок не поддерживает правила по папкам")
	}
	return c.changeManualRouting(func(state *routing.RoutingState) error {
		return state.ChangeRule(RoutingMode(mode), category, value, false)
	})
}

func (c *Core) RemoveRule(mode, category, value string) error {
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	return c.changeManualRouting(func(state *routing.RoutingState) error {
		return state.ChangeRule(RoutingMode(mode), category, value, true)
	})
}

// ============================================================
//  Profiles
// ============================================================

func (c *Core) Profiles() ([]ProfileInfo, error) { return c.profiles.List() }
func (c *Core) ActiveProfile() (string, error)   { return c.profiles.Active() }
func (c *Core) RenameProfile(name, label string) error {
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	return c.profiles.Rename(name, label)
}

// DeleteProfile removes a profile and also drops its uptime history so the
// file stays tidy. Triggers a config rebuild so the deleted profile drops
// out of the selector group; restarts the engine if it was running (sing-
// box doesn't hot-remove outbounds either).
func (c *Core) DeleteProfile(name string) error {
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	// Check existence before stopping a running connection or changing files.
	_, found, err := c.profiles.Get(name)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("профиль %q не найден", name)
	}
	wasRunning := c.IsRunning()
	if wasRunning {
		if err := c.backend.Stop(); err != nil {
			return err
		}
	}
	if err := c.profiles.Delete(name); err != nil {
		return err
	}
	c.uptime.Forget(name)
	c.uptime.Save()
	if err := c.rebuildConfig(); err != nil {
		return err
	}
	pf, err := c.profiles.Read()
	if err != nil {
		return err
	}
	if wasRunning && len(pf.Profiles) > 0 {
		return c.startVPN()
	}
	return nil
}

// SwitchProfile marks the named profile active and propagates the change:
//   - profiles.json: active = name
//   - config.json: selector default = profile tag (used on next start)
//   - running engine (if any): instant Clash-API select, no restart
func (c *Core) SwitchProfile(name string) error {
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	if err := c.profiles.SetActive(name); err != nil {
		return err
	}
	if err := c.rebuildConfig(); err != nil {
		return err
	}
	if !c.backend.IsRunning() {
		return nil
	}
	if c.backend.Info().Capabilities.LiveProfileSwitch {
		return c.backend.SelectProfile(name)
	}
	return c.restartVPN()
}

// AddProfileFromURI parses a server URI or subscription URL and appends
// the resulting profile(s). The new profile(s) are added to config.json's
// selector group so they can be probed for liveness immediately.
func (c *Core) AddProfileFromURI(uri string) (string, error) {
	deviceID, err := c.subscriptionDeviceID()
	if err != nil {
		return "", err
	}
	ctx := c.subscriptionContext()
	imports, err := subscription.ParseDetailedContext(ctx, uri, deviceID, c.backend.Decode)
	if err != nil {
		return "", err
	}
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	before, err := c.profiles.Read()
	if err != nil {
		return "", err
	}
	after, err := cloneProfiles(before)
	if err != nil {
		return "", err
	}
	first := ""
	for _, imported := range imports {
		drafts := subscriptionDrafts(imported.Items)
		var names []string
		if imported.SourceURL != "" {
			index, err := ensureSubscription(after, imported.SourceURL, drafts)
			if err != nil {
				return "", err
			}
			source := &after.Subscriptions[index]
			source.Info = subscriptionInfo(source.ID, imported.Metadata)
			source.LastAttempt = time.Now().Unix()
			source.LastSuccess = source.LastAttempt
			source.NextAttempt = source.LastSuccess + int64(subscriptionPollInterval/time.Second)
			source.Failures = 0
			source.Revision++
			names, err = profile.ReconcileSubscription(after, source, drafts)
			if err != nil {
				return "", err
			}
		} else {
			names, err = profile.AppendConnections(after, drafts)
			if err != nil {
				return "", err
			}
		}
		if first == "" && len(names) > 0 {
			first = names[0]
		}
	}
	if err := c.commitProfiles(ctx, before, after); err != nil {
		return "", err
	}
	return first, nil
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
	if p.Connection != nil {
		uri := p.Connection.URI
		if u, err := url.Parse(uri); err == nil && u.Scheme != "" {
			u.Fragment = p.Label
			return u.String(), nil
		}
		return uri, nil
	}
	return c.backend.Export(p)
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
	policy, err := c.manualRouting()
	if err != nil {
		return err
	}
	effective, err := c.effectiveRouting(policy)
	if err != nil {
		return err
	}
	return c.configureRouting(effective)
}

// AutoMode reports whether auto-fastest selection is enabled.
func (c *Core) AutoMode() (bool, error) { return c.profiles.AutoMode() }

// SetAutoMode flips auto-fastest mode and propagates: writes the flag to
// profiles.json, rebuilds config.json (selector ± urltest, route.final
// adjusts), restarts the engine if running so sing-box picks it up.
func (c *Core) SetAutoMode(on bool) error {
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	if on && !c.backend.Info().Capabilities.AutoSelect {
		return fmt.Errorf("движок не поддерживает автоматический выбор сервера")
	}
	if err := c.profiles.SetAutoMode(on); err != nil {
		return err
	}
	if err := c.rebuildConfig(); err != nil {
		return err
	}
	if c.backend.IsRunning() {
		return c.restartVPN()
	}
	return nil
}

// ============================================================
//  Uptime history
// ============================================================

func (c *Core) UptimeHistory() map[string][]PingSample { return c.uptime.Snapshot() }

// ----- downdetector -----

func (c *Core) DowndetectorSettings() downdetector.Settings { return c.downdetector.Settings() }

func (c *Core) SetDowndetectorSettings(s downdetector.Settings) error {
	return c.downdetector.SetSettings(c.downdetectorSettingsFollowingSwitch(s))
}

// DowndetectorLastReport is the latest report exactly as it would be sent,
// for the "what is sent" screen. nil until the first report is built.
func (c *Core) DowndetectorLastReport() *downdetector.Report { return c.downdetector.LastReport() }

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

	var wg sync.WaitGroup
	for i, svc := range services {
		wg.Add(1)
		go func(i int, svc ServicePingTarget) {
			defer wg.Done()
			ms, err := c.backend.Delay(activeTag, svc.URL, uptime.PingTimeoutMs)
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
		c.serviceUptime.Record(activeTag+"|"+svc.Name, results[i].OK, results[i].RttMs, uptime.SrcTunnel)
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
