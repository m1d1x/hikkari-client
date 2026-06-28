// app.go is the Wails binding surface. It's a thin adapter around
// core/api.Core — every method here is a one-liner delegation. Keep it
// boring on purpose: this file is the UI contract for the Svelte frontend,
// and any logic added here also has to be added to the Flutter side later.
//
// Real logic lives in core/api and the managers under core/*.
package main

import (
	"context"
	"os"
	"path/filepath"

	"NekkoVPN/core/api"
)

// App is the Wails-bound facade. It owns a single api.Core; every bound
// method just delegates.
type App struct {
	core *api.Core
}

func NewApp() *App { return &App{core: api.New()} }

func (a *App) startup(_ context.Context) {
	if err := a.core.Init(findBaseDir()); err != nil {
		// Init only fails for empty baseDir or double-init — neither should
		// happen here, but log to stderr so it's visible if it ever does.
		println("nekkobox init failed:", err.Error())
	}
}

func (a *App) shutdown(_ context.Context) { _ = a.core.Shutdown() }

// findBaseDir locates the folder that holds sing-box.exe + config.json.
// Lives here (not in api.Core) because the search strategy is desktop-only —
// mobile builds pass baseDir explicitly from Kotlin/Swift.
func findBaseDir() string {
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

// ============================================================
//  Wails bindings — names are part of the UI contract.
//  Type aliases keep the generated TypeScript shapes identical.
// ============================================================

type (
	HealthInfo  = api.HealthInfo
	Whitelist   = api.Whitelist
	ProfileInfo = api.ProfileInfo
	PingSample  = api.PingSample
)

func (a *App) StartVPN() error    { return a.core.StartVPN() }
func (a *App) StopVPN() error     { return a.core.StopVPN() }
func (a *App) RestartVPN() error  { return a.core.RestartVPN() }
func (a *App) IsRunning() bool    { return a.core.IsRunning() }
func (a *App) Health() HealthInfo { return a.core.Health() }
func (a *App) ExternalIP() string { return a.core.ExternalIP() }
func (a *App) GetLogTail() string { return a.core.LogTail() }

func (a *App) GetWhitelist() (Whitelist, error)        { return a.core.Whitelist() }
func (a *App) AddRule(category, value string) error    { return a.core.AddRule(category, value) }
func (a *App) RemoveRule(category, value string) error { return a.core.RemoveRule(category, value) }

func (a *App) GetProfiles() ([]ProfileInfo, error)       { return a.core.Profiles() }
func (a *App) ActiveProfile() (string, error)            { return a.core.ActiveProfile() }
func (a *App) RenameProfile(name, newLabel string) error { return a.core.RenameProfile(name, newLabel) }
func (a *App) DeleteProfile(name string) error           { return a.core.DeleteProfile(name) }
func (a *App) SwitchProfile(name string) error           { return a.core.SwitchProfile(name) }

func (a *App) GetUptimeHistory() map[string][]PingSample { return a.core.UptimeHistory() }
