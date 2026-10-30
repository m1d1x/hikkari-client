// Package api is the single public surface every UI uses to talk to the
// nekkobox engine. Two layers sit on top of one Core:
//
//   - Typed Go methods (methods.go) — used directly by gomobile bind on
//     Android/iOS.
//   - JSON dispatch (dispatch.go) — Core.Call(method, argsJSON) returns
//     resultJSON inside an envelope. This is what cgo exports as a single
//     C function for dart:ffi on the desktop side.
//
// The contract is documented in contract.md alongside this file.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"NekkoVPN/core/cleanliness"
	"NekkoVPN/core/downdetector"
	"NekkoVPN/core/engine"
	"NekkoVPN/core/geoip"
	"NekkoVPN/core/health"
	"NekkoVPN/core/logging"
	"NekkoVPN/core/netinfo"
	"NekkoVPN/core/presets"
	"NekkoVPN/core/profile"
	"NekkoVPN/core/profile/legacy"
	"NekkoVPN/core/routing"
	"NekkoVPN/core/uptime"
)

// Core holds every long-lived component the engine needs. Construct with New(),
// then call Init() (or InitWithVPN for custom platforms) before any typed
// method or Call().
type Core struct {
	mu               sync.Mutex
	stopping         bool
	workerDone       chan struct{}
	operationMu      sync.Mutex
	presets          *presets.Manager
	routingPreview   bool
	routingApp       *routingApplication
	presetDone       chan struct{}
	subscriptionDone chan struct{}
	subscriptionWake chan struct{}
	presetState      presets.State
	baseDir          string
	ctx              context.Context
	cancel           context.CancelFunc

	logger      *logging.Logger
	routing     *routing.Store
	profiles    *profile.Store
	backend     engine.Backend
	health      *health.Checker
	cleanliness *cleanliness.Checker
	uptime      *uptime.Tracker
	// serviceUptime holds on-demand pings of arbitrary user services
	// (YouTube, Discord, …) measured through the active profile — kept
	// separate from the per-profile uptime tracker above.
	serviceUptime *uptime.Tracker
	// downdetector turns uptime samples into anonymous reports for the
	// shared downdetector (queued locally only, for now).
	downdetector *downdetector.Agent

	customServicesMu   sync.RWMutex
	customServicesPath string
	customServices     []uptime.ServiceTarget
}

// New returns an uninitialised Core. Call Init before use.
func New() *Core { return &Core{} }

// Init wires up the engine for the default (desktop) platform.
func (c *Core) Init(baseDir string) error {
	return c.InitWithEngine(baseDir, DefaultEngineFactory)
}

// InitWithEngine injects a complete engine adapter. UI and application services
// use this same contract for every engine; only the composition root chooses one.
func (c *Core) InitWithEngine(baseDir string, factory engine.Factory) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopping {
		return errors.New("предыдущий движок ещё останавливается")
	}
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
		factory = DefaultEngineFactory
	}

	c.baseDir = baseDir
	c.ctx, c.cancel = context.WithCancel(context.Background())

	if err := os.MkdirAll(baseDir, 0755); err != nil {
		c.cancel()
		c.ctx = nil
		return err
	}
	c.logger = logging.New(filepath.Join(baseDir, "nekko-vpn.log"))
	backend, err := factory(baseDir, c.logger)
	if err != nil {
		c.cancel()
		c.ctx = nil
		_ = c.logger.Close()
		return err
	}
	if backend == nil {
		c.cancel()
		c.ctx = nil
		_ = c.logger.Close()
		return errors.New("engine factory returned no adapter")
	}
	c.backend = backend
	c.routing = routing.New(filepath.Join(baseDir, "routing.json"))
	if c.routingPreview {
		c.routing = routing.NewMemory()
	}
	manual, err := c.routing.ReadRouting()
	if err != nil {
		c.cancel()
		c.ctx = nil
		_ = c.logger.Close()
		return err
	}
	c.routingApp = newRoutingApplication(manual, c.routingPreview)
	presetPath := filepath.Join(baseDir, "routing-presets.json")
	if c.routingPreview {
		presetPath = ""
	}
	presetContext := c.ctx
	c.presets, err = presets.New(presetPath, presets.Options{
		URL:       presetEndpoint("HIKKARI_PRESET_CATALOG_URL", presets.CatalogURL),
		EventsURL: presetEndpoint("HIKKARI_PRESET_EVENTS_URL", presets.EventsURL),
		Transact: func(old, next presets.State, persist func() error) error {
			return c.commitPresetState(presetContext, old, next, persist)
		},
	})
	if err != nil {
		c.cancel()
		c.ctx = nil
		_ = c.logger.Close()
		return err
	}
	c.presetState = c.presets.Snapshot().State
	c.profiles = profile.New(filepath.Join(baseDir, "profiles.json"))
	c.health = health.New(
		filepath.Join(baseDir, "direct-ip.txt"),
		c.logger,
		c.vpnHosts,
		c.backend.IsRunning,
	)
	c.uptime = uptime.New(filepath.Join(baseDir, "uptime-history.json"))
	c.serviceUptime = uptime.New(filepath.Join(baseDir, "service-uptime-history.json"))
	c.downdetector = downdetector.New(
		filepath.Join(baseDir, "downdetector.json"),
		filepath.Join(baseDir, "downdetector-outbox.json"),
		downdetector.Deps{
			Servers: c.downdetectorServers,
			History: c.uptime.Snapshot,
			Net:     c.downdetectorNet,
		},
	)
	// Server-cleanliness scoring. Gated on TunnelOK so we never score the
	// bare ISP IP. IPQS key is optional — dropped as plain text in
	// baseDir/ipqs-key.txt; absent → fraud-score step is simply skipped.
	c.cleanliness = cleanliness.New(
		c.logger,
		func() bool { return c.health.Probe().TunnelOK },
		readOptionalKey(filepath.Join(baseDir, "ipqs-key.txt")),
	)
	c.cleanliness.SetChangeKey(func() string {
		ip := c.health.Probe().IP
		if ip == "" {
			return ""
		}
		return ip + "|" + c.activeProfileProvider()
	})

	c.health.LoadDirectIP()
	c.ensureProfiles()
	configurationErr := c.ensureEngineConfiguration()
	initialEffective, initialErr := c.effectiveRouting(manual)
	if initialErr != nil {
		c.publishRouting("failed", initialErr, manual, manual, false)
	} else {
		if configurationErr != nil {
			c.publishRouting("failed", configurationErr, manual, initialEffective, false)
		} else {
			c.publishRouting("idle", nil, manual, initialEffective, false)
		}
	}
	c.uptime.Load()
	c.serviceUptime.Load()
	c.downdetector.Load()
	c.customServicesPath = filepath.Join(baseDir, "custom-services.json")
	c.loadCustomServices()
	geoip.Load(filepath.Join(baseDir, "geoip-cache.json"))
	c.migrateServiceHistory()
	probe := uptime.NewLiveProbe(c.backend)
	// Join every background observer before this Core can be initialized again.
	// Their providers refer to Core fields, so cancellation alone is insufficient.
	ctx, tracker, serviceTracker, checker, cleanliness, backend, reporter := c.ctx, c.uptime, c.serviceUptime, c.health, c.cleanliness, c.backend, c.downdetector
	var workers sync.WaitGroup
	run := func(fn func()) { workers.Add(1); go func() { defer workers.Done(); fn() }() }
	run(func() { tracker.Run(ctx, c.uptimeHosts, probe) })
	run(func() { serviceTracker.RunServices(ctx, c.activeProfileProvider, c.allServiceTargets, backend) })
	run(func() { reporter.Run(ctx) })
	run(func() { checker.Run(ctx) })
	run(func() { cleanliness.Run(ctx) })
	if !c.routingPreview {
		run(backend.Baseline)
	}
	c.workerDone = make(chan struct{})
	workerDone := c.workerDone
	go func() { workers.Wait(); close(workerDone) }()
	presetManager, presetCtx := c.presets, c.ctx
	c.presetDone = make(chan struct{})
	presetDone := c.presetDone
	go func() { defer close(presetDone); presetManager.Run(presetCtx) }()

	c.subscriptionWake = make(chan struct{}, 1)
	c.subscriptionDone = make(chan struct{})
	syncContext, syncWake, syncDone := c.ctx, c.subscriptionWake, c.subscriptionDone
	go func() { defer close(syncDone); c.runSubscriptions(syncContext, syncWake) }()

	c.logger.Logf("=== api.Core init, baseDir=%s ===", baseDir)
	c.logEnvironment()
	c.startUpdates()
	return nil
}

// Shutdown cancels background loops, stops the VPN core, and closes the log.
// Safe to call more than once.
func (c *Core) Shutdown() error {
	c.operationMu.Lock()
	c.mu.Lock()
	if c.stopping {
		c.mu.Unlock()
		c.operationMu.Unlock()
		return errors.New("движок уже останавливается")
	}
	if c.ctx == nil {
		c.mu.Unlock()
		c.operationMu.Unlock()
		return nil
	}
	c.stopping = true
	c.logger.Logf("=== api.Core shutdown ===")
	var stopErr error
	if !c.routingPreview {
		stopErr = c.backend.Stop()
	}
	if stopErr == nil {
		stopErr = c.flushRoutingLocked()
	}
	c.cancelRoutingTimer()
	c.cancel()
	if stopErr != nil {
		c.logger.Logf("shutdown: engine stop/flush failed: %v", stopErr)
	}
	logger := c.logger
	subscriptionDone, presetDone, workerDone := c.subscriptionDone, c.presetDone, c.workerDone
	c.subscriptionWake = nil
	c.ctx, c.cancel = nil, nil
	c.mu.Unlock()
	c.operationMu.Unlock()
	// Canceled callbacks may still need operationMu to finish rollback.
	if presetDone != nil {
		<-presetDone
	}
	if subscriptionDone != nil {
		<-subscriptionDone
	}
	if workerDone != nil {
		<-workerDone
	}
	_ = logger.Close()
	c.mu.Lock()
	c.stopping = false
	c.mu.Unlock()
	return stopErr
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
	pf, err := c.profiles.Read()
	if err != nil {
		c.logger.Logf("profiles: read failed: %v", err)
		return
	}
	changed := false
	if !c.profiles.Exists() {
		stored, err := legacy.ReadConfig(filepath.Join(c.baseDir, "config.json"))
		if err != nil {
			c.logger.Logf("profiles: legacy import failed: %v", err)
		} else if len(stored) > 0 {
			pf.Profiles = stored
			pf.Active = stored[0].Name
			changed = true
		}
	}

	for i, p := range pf.Profiles {
		if p.Connection != nil {
			continue
		}
		normalized, err := legacy.Normalize(p)
		if err != nil {
			c.logger.Logf("profiles: could not normalize %q: %v", p.Name, err)
			continue
		}
		pf.Profiles[i] = normalized
		changed = true
	}
	if changed {
		if err := c.profiles.Write(pf); err != nil {
			c.logger.Logf("profiles: migration failed: %v", err)
		}
	}
}

func (c *Core) ensureEngineConfiguration() error {
	if err := c.rebuildConfig(); err != nil {
		c.logger.Logf("engine: configuration failed: %v", err)
		return err
	}
	return nil
}

func (c *Core) migrateServiceHistory() {
	pf, err := c.profiles.Read()
	if err != nil {
		return
	}
	scopes := legacy.ServiceScopes(pf.Profiles)
	c.serviceUptime.MigrateScopes(scopes)
	c.serviceUptime.Save()
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
			Profile:   p.Name,
			ProfileID: p.Name,
			Host:      host,
			Port:      port,
		})
	}
	return out
}

// downdetectorServers lists every profile as a reportable server. Only
// subscription servers are shared by default; the user's own servers
// (single links, pasted configs) need the share_private opt-in.
func (c *Core) downdetectorServers() []downdetector.Server {
	pf, err := c.profiles.Read()
	if err != nil {
		return nil
	}
	out := make([]downdetector.Server, 0, len(pf.Profiles))
	for _, p := range pf.Profiles {
		host, port := profile.PeerHostPort(p)
		if host == "" {
			continue
		}
		proto := profile.ProtoSlug(p)
		out = append(out, downdetector.Server{
			Profile: p.Name,
			ID:      downdetector.ServerID(host, port, proto),
			Proto:   proto,
			Private: !p.Shared(),
		})
	}
	return out
}

// downdetectorNet resolves the ISP baseline IP to provider + area, locally.
// The IP itself stays here.
func (c *Core) downdetectorNet() downdetector.Net {
	ip := net.ParseIP(c.health.DirectIP())
	if ip == nil {
		return downdetector.Net{}
	}
	country, _ := geoip.LookupIP(ip)
	ni := netinfo.Lookup(ip)
	return downdetector.Net{ASN: ni.ASN, Country: country, Region: ni.Area}
}

func (c *Core) activeProfileProvider() string {
	active, err := c.profiles.Active()
	if err != nil || active == "" {
		return ""
	}
	return active
}

func (c *Core) allServiceTargets() []uptime.ServiceTarget {
	defaults := defaultServiceTargets()
	c.customServicesMu.RLock()
	custom := make([]uptime.ServiceTarget, len(c.customServices))
	copy(custom, c.customServices)
	c.customServicesMu.RUnlock()
	return append(defaults, custom...)
}

func (c *Core) loadCustomServices() {
	data, err := os.ReadFile(c.customServicesPath)
	if err != nil {
		return
	}
	var svcs []uptime.ServiceTarget
	if json.Unmarshal(data, &svcs) == nil {
		c.customServices = svcs
	}
}

func (c *Core) saveCustomServices() error {
	data, err := json.MarshalIndent(c.customServices, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.customServicesPath, data, 0644)
}

// readOptionalKey returns the trimmed contents of a small text file, or ""
// if it's missing/unreadable. Used for the optional IPQS API key so the
// secret lives on disk (baseDir/ipqs-key.txt) instead of in code or config.
func readOptionalKey(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func defaultServiceTargets() []uptime.ServiceTarget {
	return []uptime.ServiceTarget{
		{Name: "YouTube", URL: "https://youtube.com/generate_204"},
		{Name: "Google", URL: "https://google.com/generate_204"},
		{Name: "Telegram", URL: "https://telegram.org/generate_204"},
		{Name: "Discord", URL: "https://discord.com/generate_204"},
		{Name: "ChatGPT", URL: "https://chatgpt.com/generate_204"},
		{Name: "Instagram", URL: "https://instagram.com/generate_204"},
		{Name: "Steam", URL: "https://store.steampowered.com/generate_204"},
		{Name: "X", URL: "https://x.com/generate_204"},
	}
}
