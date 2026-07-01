// Package api is the single public surface every UI uses to talk to the
// nekkobox engine. Two layers sit on top of one Core:
//
//   - Typed Go methods (methods.go) — used directly by Wails-style bindings
//     and by gomobile bind on Android/iOS.
//   - JSON dispatch (dispatch.go) — Core.Call(method, argsJSON) returns
//     resultJSON inside an envelope. This is what cgo exports as a single
//     C function for dart:ffi on the desktop side.
//
// The contract is documented in contract.md alongside this file.
package api

import (
	"context"
	"errors"
	"path/filepath"
	"sync"

	"NekkoVPN/core/clashapi"
	"NekkoVPN/core/configfile"
	"NekkoVPN/core/geoip"
	"NekkoVPN/core/health"
	"NekkoVPN/core/logging"
	"NekkoVPN/core/profile"
	"NekkoVPN/core/uptime"
	"NekkoVPN/core/vpn"
)

// VPNFactory builds the platform-appropriate Manager given the base directory
// and a logger. The default factory (used by Init) constructs a ProcessManager
// — on desktop that runs sing-box.exe as a child process; on mobile we'll
// inject a libbox-backed Manager via InitWithVPN instead.
type VPNFactory func(baseDir string, log vpn.LineLogger) vpn.Manager

// DefaultVPNFactory is the desktop ProcessManager factory.
func DefaultVPNFactory(baseDir string, log vpn.LineLogger) vpn.Manager {
	return vpn.NewProcessManager(
		filepath.Join(baseDir, "sing-box.exe"),
		filepath.Join(baseDir, "config.json"),
		baseDir,
		log,
	)
}

// Core holds every long-lived component the engine needs. Construct with New(),
// then call Init() (or InitWithVPN for custom platforms) before any typed
// method or Call().
type Core struct {
	mu      sync.Mutex
	baseDir string
	ctx     context.Context
	cancel  context.CancelFunc

	logger   *logging.Logger
	configs  *configfile.Store
	profiles *profile.Store
	vpn      vpn.Manager
	health   *health.Checker
	uptime   *uptime.Tracker
	// serviceUptime holds on-demand pings of arbitrary user services
	// (YouTube, Discord, …) measured through the active profile — kept
	// separate from the per-profile uptime tracker above.
	serviceUptime *uptime.Tracker
}

// New returns an uninitialised Core. Call Init before use.
func New() *Core { return &Core{} }

// Init wires up the engine for the default (desktop) platform.
func (c *Core) Init(baseDir string) error {
	return c.InitWithVPN(baseDir, DefaultVPNFactory)
}

// InitWithVPN wires up the engine using the given VPN factory. Mobile builds
// pass a factory that returns a libbox-backed Manager.
func (c *Core) InitWithVPN(baseDir string, factory VPNFactory) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if baseDir == "" {
		return errors.New("api.Core: baseDir is required")
	}
	if c.ctx != nil {
		// Idempotent re-init: tolerated when the baseDir matches (this is
		// the common case under Flutter hot restart, where the Dart isolate
		// reboots but the host DLL is still loaded). A *different* baseDir
		// is still a programming error — refuse it loudly.
		if c.baseDir == baseDir {
			return nil
		}
		return errors.New("api.Core: already initialised with a different baseDir")
	}
	if factory == nil {
		factory = DefaultVPNFactory
	}

	c.baseDir = baseDir
	c.ctx, c.cancel = context.WithCancel(context.Background())

	c.logger = logging.New(filepath.Join(baseDir, "nekko-vpn.log"))
	c.configs = configfile.New(filepath.Join(baseDir, "config.json"))
	c.profiles = profile.New(filepath.Join(baseDir, "profiles.json"))
	c.vpn = factory(baseDir, c.logger)
	c.health = health.New(
		filepath.Join(baseDir, "direct-ip.txt"),
		c.logger,
		c.vpnHosts,
	)
	c.uptime = uptime.New(filepath.Join(baseDir, "uptime-history.json"))
	c.serviceUptime = uptime.New(filepath.Join(baseDir, "service-uptime-history.json"))

	c.health.LoadDirectIP()
	c.ensureProfiles()
	c.ensureSelectorConfig()
	c.uptime.Load()
	c.serviceUptime.Load()
	geoip.Load(filepath.Join(baseDir, "geoip-cache.json"))
	probe := uptime.NewLiveProbe(clashapi.New(""))
	go c.uptime.Run(c.ctx, c.uptimeHosts, probe)
	// Health probing is the slow path (HTTPS to api.ipify.org). Running it
	// in the background keeps FFI calls instant — UI threads on the other
	// side of the FFI boundary must never wait on network.
	go c.health.Run(c.ctx)

	c.logger.Logf("=== api.Core init, baseDir=%s ===", baseDir)
	return nil
}

// Shutdown cancels background loops, stops the VPN core, and closes the log.
// Safe to call more than once.
func (c *Core) Shutdown() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ctx == nil {
		return nil
	}
	c.logger.Logf("=== api.Core shutdown ===")
	c.cancel()
	_ = c.vpn.Stop()
	_ = c.logger.Close()
	c.ctx = nil
	c.cancel = nil
	return nil
}

// BaseDir reports the directory the engine was initialised with.
func (c *Core) BaseDir() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.baseDir
}

// ensureProfiles seeds profiles.json from the current config.json on first
// run, so the UI always has at least one profile to render.
func (c *Core) ensureProfiles() {
	if c.profiles.Exists() {
		return
	}
	cfg, err := c.configs.Read()
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
	_ = c.profiles.SeedFromEndpoint(ep)
}

// ensureSelectorConfig migrates pre-selector config.json layouts to the
// new shape on first start after the refactor. After this returns, the
// outbounds list contains every known profile + a "proxy" selector, and
// route.final / dns.detour point at it.
func (c *Core) ensureSelectorConfig() {
	if !c.configs.NeedsSelectorMigration() {
		return
	}
	c.logger.Logf("config: migrating to selector layout")
	if err := c.rebuildConfig(); err != nil {
		c.logger.Logf("config: migration failed: %v", err)
	}
}

// vpnHosts feeds the health checker the list of server IPs it must never
// cache as the "ISP baseline".
func (c *Core) vpnHosts() []string {
	pf, err := c.profiles.Read()
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(pf.Profiles))
	for _, p := range pf.Profiles {
		if h := profile.PeerHost(p); h != "" {
			out = append(out, h)
		}
	}
	return out
}

// uptimeHosts feeds the uptime tracker everything it needs per profile:
// a stable name (history key), the sing-box tag (Clash-API path), and a
// host:port pair for the TCP-connect fallback when the engine is off.
func (c *Core) uptimeHosts() []uptime.HostEntry {
	pf, err := c.profiles.Read()
	if err != nil {
		return nil
	}
	out := make([]uptime.HostEntry, 0, len(pf.Profiles))
	for _, p := range pf.Profiles {
		host, port := profile.PeerHostPort(p)
		out = append(out, uptime.HostEntry{
			Profile: p.Name,
			Tag:     profile.ProfileTag(p.Name),
			Host:    host,
			Port:    port,
		})
	}
	return out
}
