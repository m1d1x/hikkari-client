// Typed Core methods. These are what gomobile bind exports as native methods
// on mobile, and what the JSON dispatcher in dispatch.go ultimately calls.
// Keep this file boring on purpose — pure delegation to the underlying
// managers. Any business logic belongs in core/* packages, not here.

package api

import (
	"NekkoVPN/core/supportqueue"
	"NekkoVPN/core/testerlog"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"NekkoVPN/core/applist"
	"NekkoVPN/core/appversion"
	"NekkoVPN/core/cleanliness"
	"NekkoVPN/core/downdetector"
	"NekkoVPN/core/engine"
	"NekkoVPN/core/geoip"
	"NekkoVPN/core/health"
	"NekkoVPN/core/logging"
	"NekkoVPN/core/profile"
	"NekkoVPN/core/routing"
	"NekkoVPN/core/subscription"
	"NekkoVPN/core/uptime"
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
		Profiles: pf.Profiles, ActiveProfile: pf.Active, AutoSelect: pf.Auto, AutoProfiles: pf.AutoProfiles(), AutoPreference: pf.AutoPreference(), Routing: effective,
	}, c.backend.Info().Capabilities)
}

// ConnectOutcome is what a press of «Подключиться» did besides the start.
type ConnectOutcome struct {
	// ForeignVPNStopped are the other VPN clients whose tunnel was ended
	// first, for the UI to say so.
	ForeignVPNStopped []string `json:"foreignVpnStopped,omitempty"`
}

func (c *Core) StartVPN() error {
	_, err := c.Connect()
	return err
}

// Connect is the user's start. Another VPN client's tunnel is ended first:
// with two tunnels up neither works (core/foreignvpn). The host selection
// is dropped then, since the direct path it found ran under that tunnel.
func (c *Core) Connect() (ConnectOutcome, error) {
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	if err := c.flushRoutingLocked(); err != nil {
		return ConnectOutcome{}, err
	}
	// A press that waited for superviseCore finds the core already up; starting
	// it again would be a second break for nothing.
	if !c.routingPreview && c.backend.IsRunning() {
		return ConnectOutcome{}, nil
	}
	var outcome ConnectOutcome
	if !c.routingPreview && c.stopForeignVPN != nil {
		outcome.ForeignVPNStopped = c.stopForeignVPN()
		if len(outcome.ForeignVPNStopped) > 0 {
			c.forgetHostLocked()
		}
	}
	started := time.Now()
	err := c.startVPN()
	if err != nil {
		c.SubscriptionWake()
	}
	if !c.routingPreview {
		c.reportConnect(started, err)
	}
	return outcome, err
}

func (c *Core) startVPN() (err error) {
	if c.routingPreview {
		return fmt.Errorf("режим предпросмотра не запускает VPN")
	}
	started := time.Now()
	defer func() { c.logConnect("start", started, err) }()
	if err := c.requireProfiles(); err != nil {
		return err
	}
	c.sanitizeSystemProxy()
	c.selectHostLocked()
	if err := c.rebuildConfig(); err != nil {
		return err
	}
	// The core reads the autotunnel rule set the service owns; push what this
	// side knows before the start request rewrites the config around it.
	if c.autoTunnel != nil {
		if err := c.autoTunnel.Publish(); err != nil && c.logger != nil {
			c.logger.Logf("[autotunnel] publish before start: %v", err)
		}
	}
	if !c.backend.Info().Capabilities.TUN {
		return fmt.Errorf("выбранный адаптер не поддерживает TUN на этой ОС; требуется реализация TUN для выбранного адаптера")
	}
	return c.backend.Start(c.ctx)
}

// sanitizeSystemProxy turns off a system proxy that a crashed proxy tool
// left pointing at a dead local port: browsers, Steam and Discord follow it
// and fail on everything the routing sends direct. It never blocks a
// connect — a failure is only logged.
func (c *Core) sanitizeSystemProxy() {
	a, err := sanitizeHostProxy()
	if c.logger == nil {
		return
	}
	switch {
	case err != nil:
		c.logger.Logf("[netdoctor] system proxy check failed: %v", err)
	case a.Done:
		c.logger.Logf("[netdoctor] disabled orphan system proxy")
	}
}

func (c *Core) StopVPN() error {
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	if !c.routingPreview {
		if err := c.backend.Stop(); err != nil {
			return err
		}
		c.health.MarkStopped()
		c.logConnect("stop", time.Now(), nil)
		go c.measureTunnelDown()
	}
	return c.flushRoutingLocked()
}
func (c *Core) RestartVPN() error {
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	return c.restartVPNLocked("api-restart")
}

// restartVPNLocked: callers hold operationMu.
func (c *Core) restartVPNLocked(reason string) error {
	wasRunning := c.backend.IsRunning()
	pendingRestart := c.routingApp.pending != nil && wasRunning && !routing.ActiveEqual(c.routingApp.pending.effective, c.RoutingStatus().Effective)
	if err := c.flushRoutingLocked(); err != nil {
		return err
	}
	if pendingRestart {
		return nil
	}
	return c.restartVPN(reason)
}

func (c *Core) restartVPN(reason string) (resultErr error) {
	finish := c.traceCoreTransition(reason, "")
	defer func() { finish(resultErr) }()
	if c.routingPreview {
		return fmt.Errorf("режим предпросмотра не перезапускает VPN")
	}
	if err := c.requireProfiles(); err != nil {
		return err
	}
	if err := c.stopForRestart(reason); err != nil {
		return err
	}
	return c.startVPN()
}

// stopForRestart stops the core with a start to follow: if that start fails the
// core stays wanted and superviseCore keeps trying. A user's stop is backend.Stop.
func (c *Core) stopForRestart(reason string) (resultErr error) {
	finish := c.traceCoreTransition(reason+"/stop", "")
	defer func() { finish(resultErr) }()
	if core, ok := c.backend.(engine.CoreSupervision); ok {
		return core.StopForRestart()
	}
	return c.backend.Stop()
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

// maxUILogBytes cuts a UI message before it reaches the log.
const maxUILogBytes = 2000

// LogUI records what the user was shown — an error card, a failed action —
// so a log read later (a support report, the tester stream) has the same
// words the user saw. Multi-line text stays one log line.
func (c *Core) LogUI(message string) {
	if c == nil || c.logger == nil || message == "" {
		return
	}
	if len(message) > maxUILogBytes {
		message = message[:maxUILogBytes] + "…"
	}
	c.logger.Logf("[ui] %s", strings.ReplaceAll(message, "\n", " ⏎ "))
}

func (c *Core) CollectDiagnostics(maxLines int) string {
	if c == nil || c.logger == nil {
		return ""
	}
	// The release this process runs; the installed one only stands in for
	// a build that does not know its own.
	if appversion.Own() == "" {
		if st := c.UpdateStatus(); st.Installed != "" {
			logging.AppVersion = st.Installed
		}
	}
	return c.logger.CollectDiagnostics(maxLines)
}

func supportEndpoint() string {
	if v := os.Getenv("HIKKARI_SUPPORT_URL"); v != "" {
		return v
	}
	return supportPath("/api/v1/support/report")
}

// supportPath places a support endpoint on the hub: the host of
// HIKKARI_TELEMETRY_URL when set, production otherwise.
func supportPath(path string) string {
	if v := os.Getenv("HIKKARI_TELEMETRY_URL"); v != "" {
		if u, err := url.Parse(v); err == nil {
			u.Path, u.RawQuery = path, ""
			return u.String()
		}
	}
	return "https://api.hikkari.net" + path
}

// SubmitSupportReport takes the log snapshot now and queues the report for
// delivery (core/supportqueue): it is on disk when this returns and reaches
// the hub as soon as the hub is reachable, across network flaps and restarts.
// It fails only when the report cannot be saved.
func (c *Core) SubmitSupportReport(message string) (map[string]any, error) {
	if c == nil || c.support == nil {
		return nil, fmt.Errorf("движок не запущен")
	}
	app := c.telemetryApp()
	installID := ""
	if c.telemetry != nil {
		installID = c.telemetry.InstallID()
	}
	if installID == "" {
		installID = "r-unknown"
	}
	id, err := c.support.Enqueue(supportqueue.Report{
		InstallID: installID,
		Platform:  app.OS,
		Version:   app.Version,
		Message:   message,
		Logs:      c.CollectDiagnostics(500),
	})
	if err != nil {
		c.logger.Logf("[support] report not queued: %v", err)
		return nil, err
	}
	c.logReportForTester(id, message)
	return map[string]any{"queued": true, "id": id}, nil
}

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
func (c *Core) DeleteProfile(name string) (resultErr error) {
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
	last := false
	if pf, err := c.profiles.Read(); err == nil && len(pf.Profiles) <= 1 {
		last = true
	}
	if wasRunning {
		finish := c.traceCoreTransition("profile-delete", "")
		defer func() { finish(resultErr) }()
		// Deleting the last profile ends the wish to be up: nothing can start.
		if last {
			if err := c.backend.Stop(); err != nil {
				return err
			}
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
	if !wasRunning || last || len(pf.Profiles) == 0 {
		return nil
	}
	// The other profiles keep their connections when the core drops this
	// one in place; otherwise it restarts without it.
	if c.reloadInPlace(c.ctx, "profile-delete") {
		return nil
	}
	if err := c.stopForRestart("profile-delete"); err != nil {
		return err
	}
	return c.startVPN()
}

// SwitchProfile marks the named profile active and propagates the change:
//   - profiles.json: active = name
//   - config.json: selector default = profile tag (used on next start)
//   - running engine (if any): instant Clash-API select, no restart
func (c *Core) SwitchProfile(name string) error {
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	before, err := c.profiles.Read()
	if err != nil {
		return err
	}
	if before.Auto {
		after, err := cloneProfiles(before)
		if err != nil {
			return err
		}
		found := false
		for _, p := range after.Profiles {
			if p.Name == name {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("профиль не найден: %s", name)
		}
		after.Active, after.Auto, after.AutoSubscriptionID = name, false, ""
		return c.commitProfiles(c.subscriptionContext(), before, after)
	}
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
		started := time.Now()
		err := c.backend.SelectProfile(name)
		c.logConnect("switch", started, err)
		c.probeAfterSwitch(err)
		return err
	}
	return c.restartVPN("profile-switch-fallback")
}

// logConnect puts what the user just did on one line: which profile, which
// protocol, how it went. A log has to say which of three profiles a tester
// pressed before the [diag:*] lines around it mean anything.
func (c *Core) logConnect(action string, started time.Time, err error) {
	if c.logger == nil {
		return
	}
	label := "?"
	if pf, readErr := c.profiles.Read(); readErr == nil {
		for _, p := range pf.Profiles {
			if p.Name == pf.Active {
				label = p.Label
				break
			}
		}
	}
	c.logger.Logf("[connect] %s profile=%q proto=%s secs=%.1f ok=%v err=%v",
		action, label, c.activeProto(), time.Since(started).Seconds(), err == nil, err)
}

// probeAfterSwitch measures the profile a tester just switched to. The session
// diagnostics probe about once a minute, and a tester trying three profiles
// moves on sooner: without this the log would hold no verdict on the middle one.
func (c *Core) probeAfterSwitch(err error) {
	if err != nil || !c.presets.Snapshot().Tester {
		return
	}
	if prober, ok := c.backend.(interface{ ProbeNow() }); ok {
		go prober.ProbeNow()
	}
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
			source.Capacity = nil
			if subscription.IsHikkariURL(source.URL) && imported.Metadata != nil {
				source.Capacity = imported.Metadata.Capacity
			}
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
			// The link's own flag names where the traffic exits: a cascade
			// dials an entry in Russia, which geoip would show instead.
			code := profile.LabelCountry(p)
			if code == "" {
				code = geoip.Resolve(profile.PeerHost(p))
			}
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

// AutoMode reports whether Hikkari subscription failover is enabled.
func (c *Core) AutoMode() (bool, error) { return c.profiles.AutoMode() }

// SetAutoMode grants the engine selection within one verified Hikkari source.
// Runtime and persistence use the same rollback transaction as subscription sync.
func (c *Core) SetAutoMode(on bool) error {
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	if on && !c.backend.Info().Capabilities.AutoSelect {
		return fmt.Errorf("движок не поддерживает автоматический выбор сервера")
	}
	before, err := c.profiles.Read()
	if err != nil {
		return err
	}
	after, err := cloneProfiles(before)
	if err != nil {
		return err
	}
	after.Auto, after.AutoSubscriptionID = on, ""
	if !on && before.Auto {
		if routed := c.resolveCarryingProfile(); routed != "" {
			after.Active = routed
		}
	}
	if on {
		after.AutoSubscriptionID = after.AutoSource()
		if len(after.AutoProfiles()) == 0 {
			return fmt.Errorf("авто доступно только для подписки hikkari; добавьте или обновите её")
		}
	}
	return c.commitProfiles(c.subscriptionContext(), before, after)
}

// ============================================================
//  Uptime history
// ============================================================

func (c *Core) UptimeHistory() map[string][]PingSample {
	out := c.uptime.Snapshot()
	pf, err := c.profiles.Read()
	if err != nil {
		return map[string][]PingSample{}
	}
	for _, p := range pf.Profiles {
		if p.Subscription == nil {
			continue
		}
		owned := false
		for _, source := range pf.Subscriptions {
			if source.ID == p.Subscription.ID && subscription.IsHikkariURL(source.URL) {
				owned = true
				break
			}
		}
		if owned {
			delete(out, p.Name)
			if m := pf.CapacityFor(p, time.Now()); m != nil {
				for _, point := range m.History {
					out[p.Name] = append(out[p.Name], PingSample{Ts: point.Ts, OK: point.RTTMS > 0, RttMs: point.RTTMS, Src: uptime.SrcProber})
				}
			}
		}
	}
	return out
}

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

// newTesterLog builds the tester log stream. It ships only while the tester
// switch (presets.State.Tester) is on.
func (c *Core) newTesterLog() *testerlog.Shipper {
	presetManager, tel := c.presets, c.telemetry
	return testerlog.New(testerlog.Options{
		URL:      supportPath("/api/v1/support/tester-logs"),
		Enabled:  func() bool { return presetManager.Snapshot().Tester },
		Backfill: c.logger.TailLines,
		Identity: func() (string, string, string) {
			app := c.telemetryApp()
			return tel.InstallID(), app.OS, app.Version
		},
		Commands: c.testerCommands(),
	})
}
