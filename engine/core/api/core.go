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
	"NekkoVPN/core/netdoctor"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"NekkoVPN/core/appversion"
	"NekkoVPN/core/autotunnel"
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
	"NekkoVPN/core/supportqueue"
	"NekkoVPN/core/telemetry"
	"NekkoVPN/core/testerlog"
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
	// shared downdetector.
	downdetector *downdetector.Agent
	// telemetry queues product events and scrubbed errors for the hub
	// (docs/TELEMETRY.md). Its switch also governs the downdetector.
	telemetry *telemetry.Agent
	// host is the direct-path selection for this machine (hostnet.go).
	// Guarded by operationMu.
	host netdoctor.Selection
	// testerLog streams the engine log to the hub while tester mode is on
	testerLog *testerlog.Shipper
	// stopForeignVPN ends other VPN clients' tunnels before a connect and
	// names them; nil leaves them alone (an injected engine).
	stopForeignVPN func() []string
	// autoTunnel is nil for an engine without a view of its direct traffic.
	autoTunnel *autotunnel.List
	// support delivers support reports, retrying until the hub takes them
	support *supportqueue.Queue

	// routed is the profile traffic goes through, kept by watchRoute.
	routedMu sync.RWMutex
	routed   string

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
	// Read before anything can swap the installation under this process.
	if own := appversion.Own(); own != "" {
		logging.AppVersion = own
		c.logger.Logf("app: release %s", own)
	}
	c.routeSubscriptionAroundTunnel()
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
	if gated, ok := c.backend.(engine.TesterGated); ok {
		presetManager := c.presets
		gated.SetTesterGate(func() bool { return presetManager.Snapshot().Tester })
	}
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
	c.telemetry = c.newTelemetry()
	c.telemetry.Load()
	c.syncDowndetectorSwitch()
	c.telemetry.Start(true)
	c.testerLog = c.newTesterLog()
	c.support = supportqueue.New(supportqueue.Options{
		Path: filepath.Join(baseDir, "support-outbox.json"),
		URL:  supportEndpoint(),
		Logf: c.logger.Logf,
	})
	c.logger.SetTap(c.testerLog.Tap)
	c.customServicesPath = filepath.Join(baseDir, "custom-services.json")
	c.loadCustomServices()
	geoip.Load(filepath.Join(baseDir, "geoip-cache.json"))
	c.migrateServiceHistory()
	probe := uptime.NewLiveProbe(c.backend)
	// Join every background observer before this Core can be initialized again.
	// Their providers refer to Core fields, so cancellation alone is insufficient.
	ctx, tracker, checker, cleanliness, backend, reporter, tel := c.ctx, c.uptime, c.health, c.cleanliness, c.backend, c.downdetector, c.telemetry
	var workers sync.WaitGroup
	run := func(fn func()) { workers.Add(1); go func() { defer workers.Done(); fn() }() }
	run(func() { tracker.Run(ctx, c.uptimeHosts, c.carryingProfile, probe, c.uptimeObserver()) })
	run(func() { reporter.Run(ctx) })
	reportSender := downdetector.NewSender(
		reporter,
		presetEndpoint("HIKKARI_DOWNDETECTOR_URL", downdetector.ReportsURL),
		presetEndpoint("HIKKARI_DOWNDETECTOR_TOKEN", downdetector.ReportsToken),
		nil,
	)
	run(func() { reportSender.Run(ctx) })
	run(func() { tel.Run(ctx) })
	testerLog := c.testerLog
	run(func() { testerLog.Run(ctx) })
	support := c.support
	run(func() { support.Run(ctx) })
	run(func() { checker.Run(ctx) })
	run(func() { cleanliness.Run(ctx) })
	if !c.routingPreview {
		if direct, ok := backend.(engine.DirectTraffic); ok {
			c.autoTunnel = autotunnel.OpenList(direct.AutoTunnelList(), c.logger.Logf)
			c.autoTunnel.SetPublish(c.publishAutoTunnel)
			list, logf := c.autoTunnel, c.logger.Logf
			run(func() { autotunnel.Run(ctx, list, direct.DirectConnections, logf) })
		}
		run(c.measureTunnelDown)
		run(backend.Baseline)
		if core, ok := backend.(engine.CoreSupervision); ok {
			run(func() { c.superviseCore(ctx, core) })
		}
		run(func() { c.watchNetwork(ctx) })
		run(func() { c.watchRoute(ctx) })
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

// coreSuperviseInterval is how often the GUI asks about the core: a local question
// to the service. The first time is at once, since after an update the core is
// waiting for this GUI's config.
var coreSuperviseInterval = 5 * time.Second

// coreSettleTime is how long after its start the GUI asks more often while the
// service does not answer.
const coreSettleTime = 30 * time.Second

// coreRetryMax caps the pause between attempts to bring up a wanted core.
var coreRetryMax = time.Minute

// superviseCore keeps the core the user wants. A core that is wanted and down gets
// started: after an update that is the one start that delivers the new release's
// rules, and it also covers a reconnect that failed or a core that died.
func (c *Core) superviseCore(ctx context.Context, core engine.CoreSupervision) {
	watch := coreWatch{}
	started := time.Now()
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		answered := c.superviseCoreOnce(ctx, core, &watch)
		// On macOS the updated GUI can open while launchd is still starting the
		// new service, and the tunnel is waiting for this very call.
		next := coreSuperviseInterval
		if !answered && time.Since(started) < coreSettleTime {
			next = min(next, time.Second)
		}
		timer.Reset(next)
	}
}

// coreWatch is what superviseCore remembers between checks.
type coreWatch struct {
	failures int
	retryAt  time.Time
	// configChecked: the core this GUI found running was compared with this
	// GUI's config. Once: later the GUI's config changes without a restart by
	// design (a profile added, one selected), and comparing then would break
	// the tunnel in the middle of a call.
	configChecked bool
}

// superviseCoreOnce holds operationMu from the question to the action: a user who
// pressed disconnect a moment ago is seen here, and is not reconnected. It says
// whether the service answered.
func (c *Core) superviseCoreOnce(ctx context.Context, core engine.CoreSupervision, watch *coreWatch) (answered bool) {
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	if ctx.Err() != nil {
		return true
	}
	state, err := core.CoreState()
	if err != nil {
		return false
	}
	if !state.Supervised {
		return true // The previous service, until the update replaces it.
	}
	switch {
	case state.Wanted && !state.Running:
		if time.Now().Before(watch.retryAt) {
			return true
		}
		c.logger.Logf("core is wanted and down: connecting (attempt %d)", watch.failures+1)
		err := c.flushRoutingLocked()
		if err == nil && !c.backend.IsRunning() {
			err = c.startVPN()
		}
		if err != nil {
			watch.failures++
			pause := min(coreSuperviseInterval<<min(watch.failures-1, 10), coreRetryMax)
			watch.retryAt = time.Now().Add(pause)
			c.logger.Logf("connect failed, next attempt in %s: %v", pause, err)
			return true
		}
		c.logger.Logf("core is up on this release's config")
		*watch = coreWatch{configChecked: true}
	case state.Running && !watch.configChecked:
		watch.failures, watch.retryAt = 0, time.Time{}
		watch.configChecked = true
		if state.ConfigCurrent {
			c.logger.Logf("core config matches this release")
			return true
		}
		c.logger.Logf("core config differs from this release: reconnecting")
		if err := c.restartVPNLocked("release-config-mismatch"); err != nil {
			c.logger.Logf("reconnect on the new config: %v", err)
		}
	case !state.Wanted:
		// The next start sends this GUI's config anyway.
		*watch = coreWatch{configChecked: true}
	default:
		watch.failures, watch.retryAt = 0, time.Time{}
	}
	return true
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
	logger, tel := c.logger, c.telemetry
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
	if err := tel.Close(); err != nil {
		logger.Logf("shutdown: telemetry queue: %v", err)
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

// ensureProfiles normalizes existing profiles in profiles.json if needed.
func (c *Core) ensureProfiles() {
	pf, err := c.profiles.Read()
	if err != nil {
		c.logger.Logf("profiles: read failed: %v", err)
		return
	}
	changed := false
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
// (single links, pasted configs) need the share_private opt-in. A tester's
// install reports every server it has, other services' included: that is what
// a tester is for.
func (c *Core) downdetectorServers() []downdetector.Server {
	pf, err := c.profiles.Read()
	if err != nil {
		return nil
	}
	tester := c.presets.Snapshot().Tester
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
			Private: !p.Shared() && !tester,
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
