// app.go is the Wails binding surface. Each method here is a thin delegation
// to a manager from core/*. Keep this file boring on purpose — it is what
// the Svelte frontend calls, and renaming anything breaks the UI contract.
package main

import (
	"context"
	"os"
	"path/filepath"

	"NekkoVPN/core/configfile"
	"NekkoVPN/core/health"
	"NekkoVPN/core/logging"
	"NekkoVPN/core/profile"
	"NekkoVPN/core/uptime"
	"NekkoVPN/core/vpn"
)

// App is the Wails-bound facade aggregating every core manager.
type App struct {
	ctx     context.Context
	baseDir string

	logger   *logging.Logger
	configs  *configfile.Store
	profiles *profile.Store
	vpn      *vpn.ProcessManager
	health   *health.Checker
	uptime   *uptime.Tracker
}

func NewApp() *App { return &App{} }

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.baseDir = a.findBaseDir()

	a.logger = logging.New(filepath.Join(a.baseDir, "nekko-vpn.log"))
	a.configs = configfile.New(filepath.Join(a.baseDir, "config.json"))
	a.profiles = profile.New(filepath.Join(a.baseDir, "profiles.json"))
	a.vpn = vpn.NewProcessManager(
		filepath.Join(a.baseDir, "sing-box.exe"),
		filepath.Join(a.baseDir, "config.json"),
		a.baseDir,
		a.logger,
	)
	a.health = health.New(
		filepath.Join(a.baseDir, "direct-ip.txt"),
		a.logger,
		a.vpnHosts,
	)
	a.uptime = uptime.New(filepath.Join(a.baseDir, "uptime-history.json"))

	a.health.LoadDirectIP()
	a.ensureProfiles()
	a.uptime.Load()
	go a.uptime.Run(ctx, a.uptimeHosts)

	a.logger.Logf("=== NekkoVPN startup, baseDir=%s ===", a.baseDir)
}

func (a *App) shutdown(_ context.Context) {
	a.logger.Logf("=== NekkoVPN shutdown: stopping core ===")
	_ = a.vpn.Stop()
	_ = a.logger.Close()
}

// findBaseDir locates the folder that holds sing-box.exe + config.json.
func (a *App) findBaseDir() string {
	var candidates []string
	if exe, err := os.Executable(); err == nil {
		d := filepath.Dir(exe)
		candidates = append(candidates, d, filepath.Dir(d), filepath.Dir(filepath.Dir(d)))
	}
	candidates = append(candidates, `C:\Users\CRYBVBY\PROJECTS\NEKOBOX`)
	for _, c := range candidates {
		if fileExists(filepath.Join(c, "sing-box.exe")) && fileExists(filepath.Join(c, "config.json")) {
			return c
		}
	}
	return `C:\Users\CRYBVBY\PROJECTS\NEKOBOX`
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// ensureProfiles seeds profiles.json from the current config.json (as "main")
// if the file is missing, so the UI always has at least one profile to show.
func (a *App) ensureProfiles() {
	if a.profiles.Exists() {
		return
	}
	cfg, err := a.configs.Read()
	if err != nil {
		return
	}
	eps, _ := cfg["endpoints"].([]interface{})
	if len(eps) == 0 {
		return
	}
	ep, ok := eps[0].(map[string]interface{})
	if !ok {
		return
	}
	_ = a.profiles.SeedFromEndpoint(ep)
}

// vpnHosts feeds health.Checker the list of server IPs it must never cache
// as the "ISP baseline".
func (a *App) vpnHosts() []string {
	pf, err := a.profiles.Read()
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(pf.Profiles))
	for _, p := range pf.Profiles {
		if h := profile.PeerHost(p.Endpoint); h != "" {
			out = append(out, h)
		}
	}
	return out
}

// uptimeHosts feeds uptime.Tracker the "profile -> host" pairs to probe.
func (a *App) uptimeHosts() []uptime.HostEntry {
	pf, err := a.profiles.Read()
	if err != nil {
		return nil
	}
	out := make([]uptime.HostEntry, 0, len(pf.Profiles))
	for _, p := range pf.Profiles {
		out = append(out, uptime.HostEntry{Profile: p.Name, Host: profile.PeerHost(p.Endpoint)})
	}
	return out
}

// ============================================================
//  Wails bindings — names are part of the UI contract
// ============================================================

func (a *App) StartVPN() error   { return a.vpn.Start() }
func (a *App) StopVPN() error    { return a.vpn.Stop() }
func (a *App) RestartVPN() error { return a.vpn.Restart() }
func (a *App) IsRunning() bool   { return vpn.ClashAPIRunning() }
func (a *App) ExternalIP() string { return a.health.ExternalIP() }

// HealthInfo is re-exported so Wails generates the same TS type for the
// frontend as before the refactor.
type HealthInfo = health.Info

func (a *App) Health() HealthInfo { return a.health.Probe() }

func (a *App) GetLogTail() string { return a.logger.Tail() }

// Whitelist re-exports the configfile type for Wails type generation.
type Whitelist = configfile.Whitelist

func (a *App) GetWhitelist() (Whitelist, error)        { return a.configs.GetWhitelist() }
func (a *App) AddRule(category, value string) error    { return a.configs.AddRule(category, value) }
func (a *App) RemoveRule(category, value string) error { return a.configs.RemoveRule(category, value) }

// ProfileInfo re-export keeps the TS shape identical to pre-refactor builds.
type ProfileInfo = profile.Info

func (a *App) GetProfiles() ([]ProfileInfo, error) { return a.profiles.List() }
func (a *App) ActiveProfile() (string, error)      { return a.profiles.Active() }
func (a *App) RenameProfile(name, newLabel string) error {
	return a.profiles.Rename(name, newLabel)
}

// DeleteProfile removes a profile and also drops its uptime history so the
// file stays tidy.
func (a *App) DeleteProfile(name string) error {
	if err := a.profiles.Delete(name); err != nil {
		return err
	}
	a.uptime.Forget(name)
	a.uptime.Save()
	return nil
}

// SwitchProfile swaps ONLY the endpoint block in config.json to the chosen
// server, marks it active, and (if the core is up) restarts to apply.
func (a *App) SwitchProfile(name string) error {
	endpoints, err := a.profiles.PrepareSwitch(name)
	if err != nil {
		return err
	}
	if err := a.configs.SetEndpoints(endpoints); err != nil {
		return err
	}
	if vpn.ClashAPIRunning() {
		return a.vpn.Restart()
	}
	return nil
}

// PingSample re-export keeps the TS type stable.
type PingSample = uptime.Sample

func (a *App) GetUptimeHistory() map[string][]PingSample { return a.uptime.Snapshot() }
