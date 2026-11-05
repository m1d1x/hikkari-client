// Package service is the privileged half of the desktop client: HikkariService
// on Windows (LocalSystem, core/winsvc registers it with the SCM) and on macOS
// (root, a LaunchDaemon, core/macsvc). It owns the sing-box core — TUN needs
// those rights and the GUI does not have them — and applies updates. The core
// is sing-box or Xray, whichever engine the GUI runs; one at a time.
//
// The GUI reaches it through one local endpoint (a named pipe on Windows, a
// unix socket on macOS) and sends config CONTENT, never a path: the service
// runs the core with full rights, so every path in that config would be a
// path opened as SYSTEM/root (see svcproto.Sanitize). The one file a compiled
// config names — the autotunnel rule set — is rewritten here to the service's
// own directory, and its content arrives as data (OpAutoTunnel), so the GUI
// still never names a file the service opens.
//
// Everything platform-specific — where things are, the endpoint, how the
// updater is spawned, what an applied update means for the service itself —
// comes in through Config. What is here is the same on both platforms.
package service

import (
	"NekkoVPN/core/autotunnel"
	"NekkoVPN/core/fsutil"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	vpn "NekkoVPN/core/engine/singbox/process"
	"NekkoVPN/core/logging"
	"NekkoVPN/core/svcproto"
)

// Version is stamped at build time (-ldflags -X).
var Version = "dev"

// Config is where one platform's installation keeps things and how it behaves.
type Config struct {
	// CoreDir is the service's own directory: config.json, cache.db,
	// service.log. Only the service writes there.
	CoreDir string
	// CoreBin is the sing-box binary and CoreWorkDir the directory it runs
	// in. Both are inside the installation, which only an administrator can
	// modify; an update replaces them while the core is stopped.
	CoreBin     string
	CoreWorkDir string
	// XrayBin is the bundled Xray, run in CoreDir on CoreDir/xray.json.
	// Empty: this installation has no Xray and refuses to start it.
	XrayBin string
	// SystemDNS points the system resolver at the running core, of either
	// engine: a resolver on the local network is reached on-link, past the
	// TUN routes. Required with XrayBin. Nil where the core's own routing
	// catches DNS (strict_route on Windows).
	SystemDNS SystemDNS

	// Updater is hikkari-updater; UpdaterArgs go after the command (root
	// and layout).
	Updater     string
	UpdaterArgs []string
	// PrepareCmd adjusts every child process before it starts (Windows:
	// no console window). Optional.
	PrepareCmd func(*exec.Cmd)

	// Listen opens the control endpoint; Endpoint names it in the log.
	Listen   func() (net.Listener, error)
	Endpoint string

	// OnVersion is told the installed version after an update or rollback
	// (Windows: "Apps & features"). Optional.
	OnVersion func(version string)

	// RestartAfterApply: the update replaced the service's own binary, so
	// after a swap the service exits (and the system starts the new one).
	// Serve then returns ErrRestart. The core's wanted state survives in
	// CoreDir/run. (macOS: the service lives inside the bundle. Windows: it
	// lives outside app\ and stays.)
	RestartAfterApply bool
	// ResumeCore: at start, bring the core up if it was wanted up when the
	// previous service exited (CoreDir/run). Implied by RestartAfterApply.
	ResumeCore bool
	// TrialDeadline: a version swapped in this recently that is still not
	// confirmed when the deadline passes is rolled back by the service
	// itself. On Windows the launcher supervises a fresh version; macOS has
	// no launcher. Zero disables it.
	TrialDeadline time.Duration

	// SyncBinaries brings the binaries that live outside the replaced tree
	// up to date with their copies inside it (Windows: the launcher, the
	// updater and this service beside app\). Called with no version on
	// trial and no updater running. idle: no core is up or wanted, so the
	// service may be replaced now; otherwise its binary waits, since replacing
	// it takes the tunnel down. restart: the service's own binary was replaced
	// and the platform has arranged for the new one to start once this one
	// exits — Serve then returns ErrRestart. Optional.
	SyncBinaries func(idle bool) (restart bool)

	// NetworkStack resets the OS network stack (OpResetNetwork). Nil: the
	// platform has no such reset and the op is refused.
	NetworkStack NetworkStack

	// HostRepair runs the soft repair (OpRepairNetwork) on platforms where
	// the settings belong to the machine, not to the signed-in user. Nil:
	// the GUI repairs them itself and the op is refused.
	HostRepair HostRepair

	// CoreReady says the sing-box core answers; a start waits for it, or for
	// the core's exit and its reason. Nil: its Clash API answers.
	CoreReady func() bool
}

// HostRepair is the platform's privileged soft repair.
type HostRepair interface {
	// Repair changes only what is provably orphaned. With proxyOnly it does
	// just the system proxy, as before a connect.
	Repair(proxyOnly bool) (svcproto.NetworkRepair, error)
}

// SystemDNS owns the system resolver setting on the service's behalf. Its
// backup outlives the process: Restore after a crash puts back what the user
// had before the core ran.
type SystemDNS interface {
	// Override points every network service's resolver at addr, keeping
	// the user's setting to restore.
	Override(addr string) error
	// Restore puts the user's setting back; a no-op when nothing is
	// overridden.
	Restore() error
}

// NetworkStack is the platform's network stack reset.
type NetworkStack interface {
	// StaticAdapters names the connected adapters with a manual IPv4
	// configuration, which a reset would wipe.
	StaticAdapters() ([]string, error)
	// Reset resets the stack; the result says whether that takes a reboot.
	// It carries what the tools printed, also when they failed.
	Reset() (svcproto.NetworkReset, error)
	// Scan reports, read-only, what Reset would deal with and the foreign
	// software that nothing touches.
	Scan() (svcproto.NetworkScan, error)
}

// ErrRestart: Serve returned because an update replaced the service.
// coreExitPoll is how long a dead core can leave the resolver pointed at it.
const coreExitPoll = 2 * time.Second

// awaitConfigFor is how long an updated service leaves a wanted core down for the
// new GUI to send its config, so that the core comes up once, on the new rules.
// A GUI that does not arrive costs this much tunnel, then the core comes up on the
// stored config. A version that dies on trial is rolled back well before it.
var awaitConfigFor = time.Minute

var ErrRestart = errors.New("service: restarting into the updated version")

type Server struct {
	cfg Config
	log *logging.Logger
	// cores by engine; engine names the one the GUI last started, which is
	// the one that runs while it is wanted up.
	cores   map[string]*vpn.ProcessManager
	engine  string
	updates *updates

	// Guards the cores: start/stop must not interleave. Update operations
	// that stop the core take it inside their own lock (see updates).
	mu sync.Mutex

	restart chan struct{}
}

// NewServer prepares the service's own directory and the core manager.
func NewServer(cfg Config) (*Server, error) {
	if err := os.MkdirAll(cfg.CoreDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating %s: %w", cfg.CoreDir, err)
	}
	if cfg.RestartAfterApply {
		cfg.ResumeCore = true
	}
	if cfg.XrayBin != "" && cfg.SystemDNS == nil {
		return nil, errors.New("service: Xray needs SystemDNS")
	}
	log := logging.New(filepath.Join(cfg.CoreDir, "service.log"))
	log.Logf("=== HikkariService %s, core=%s ===", Version, cfg.CoreBin)
	coreReady := cfg.CoreReady
	if coreReady == nil {
		coreReady = vpn.ClashAPIRunning
	}
	s := &Server{
		cfg: cfg,
		log: log,
		cores: map[string]*vpn.ProcessManager{
			svcproto.EngineSingBox: vpn.NewProcessManager(cfg.CoreBin, filepath.Join(cfg.CoreDir, "config.json"), cfg.CoreWorkDir, log).ReadyWhen(coreReady),
		},
		engine:  svcproto.EngineSingBox,
		restart: make(chan struct{}, 1),
	}
	if cfg.XrayBin != "" {
		s.cores[svcproto.EngineXray] = vpn.NewProcessManager(cfg.XrayBin, filepath.Join(cfg.CoreDir, "xray.json"), cfg.CoreDir, log)
	}
	s.updates = newUpdates(s)
	return s, nil
}

// Log is the service log, for the platform's own messages.
func (s *Server) Log() *logging.Logger { return s.log }

// Serve answers the control endpoint until ctx is cancelled (then it stops
// the core) or an update replaced the service (then it returns ErrRestart).
func (s *Server) Serve(ctx context.Context) error {
	l, err := s.cfg.Listen()
	if err != nil {
		return fmt.Errorf("listening on %s: %w", s.cfg.Endpoint, err)
	}
	s.log.Logf("service: listening on %s", s.cfg.Endpoint)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go s.updates.loop(ctx)
	if s.cfg.SystemDNS != nil {
		// A previous service that died with a core up left the resolver
		// pointed at a core that is gone.
		if err := s.cfg.SystemDNS.Restore(); err != nil {
			s.log.Logf("service: restoring system DNS: %v", err)
		}
	}
	// Before the core: this is the one moment replacing the service costs no
	// tunnel, on a machine whose core never stops. It restarts us at once.
	if !s.updates.settle(true) && s.cfg.ResumeCore {
		s.resume()
	}
	go s.watchCoreExit(ctx)
	go s.watchAwaitedConfig(ctx)
	go s.updates.watchTrial(ctx)

	var restarting atomic.Bool
	go func() {
		select {
		case <-ctx.Done():
		case <-s.restart:
			restarting.Store(true)
			cancel()
		}
		_ = l.Close()
	}()
	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			s.log.Logf("service: accept: %v", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		go s.handle(conn)
	}
	s.mu.Lock()
	if restarting.Load() {
		// Keep the run marker: the new service brings the core back. (After
		// a swap the core is already down.)
		s.log.Logf("service: restarting into the updated version")
	} else {
		s.log.Logf("service: stopping, taking the core down")
	}
	_ = s.stopCores()
	s.mu.Unlock()
	_ = s.log.Close()
	if restarting.Load() {
		return ErrRestart
	}
	return nil
}

func (s *Server) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
	var req svcproto.Request
	if err := svcproto.ReadMessage(conn, &req); err != nil {
		_ = svcproto.WriteMessage(conn, svcproto.Response{Error: "bad request: " + err.Error()})
		return
	}
	// An apply may wait behind a download in progress.
	_ = conn.SetDeadline(time.Now().Add(10 * time.Minute))
	resp := s.dispatch(req)
	_ = svcproto.WriteMessage(conn, resp)
	if !resp.OK {
		return
	}
	switch {
	case req.Op == svcproto.OpApplyUpdate && s.cfg.RestartAfterApply:
		s.requestRestart()
	case req.Op == svcproto.OpStop && !req.Restarting,
		req.Op == svcproto.OpConfirmUpdate, req.Op == svcproto.OpRollbackUpdate:
		// The installed version is settled, or the core no longer holds back
		// a new service binary: bring the binaries outside it in line — after
		// the answer is out, since that may restart the service.
		go s.updates.settle(false)
	}
}

// idle says that nothing would notice the service restarting. Callers hold mu.
func (s *Server) idle() bool {
	_, pid := s.running()
	return pid == 0 && !s.wanted()
}

// wanted says the core should be up: the run marker.
func (s *Server) wanted() bool {
	_, err := os.Stat(s.runMarker())
	return err == nil
}

// requestRestart makes Serve return ErrRestart.
func (s *Server) requestRestart() {
	select {
	case s.restart <- struct{}{}:
	default:
	}
}

func (s *Server) dispatch(req svcproto.Request) svcproto.Response {
	fail := func(err error) svcproto.Response {
		s.log.Logf("service: %s: %v", req.Op, err)
		return svcproto.Response{Error: err.Error()}
	}
	u := s.updates
	switch req.Op {
	case svcproto.OpCheckUpdate:
		u.requestCheck()
		return s.status()
	case svcproto.OpApplyUpdate:
		from, to, err := u.apply(req.Reconnect)
		if err != nil {
			return fail(err)
		}
		resp := s.status()
		resp.From, resp.To = from, to
		return resp
	case svcproto.OpRecoverUpdate:
		result, from, to, err := u.recover()
		if err != nil {
			return fail(err)
		}
		resp := s.status()
		resp.Recovery, resp.From, resp.To = result, from, to
		return resp
	case svcproto.OpConfirmUpdate:
		if err := u.confirm(); err != nil {
			return fail(err)
		}
		return s.status()
	case svcproto.OpRollbackUpdate:
		from, to, err := u.rollback()
		if err != nil {
			return fail(err)
		}
		resp := s.status()
		resp.From, resp.To = from, to
		return resp
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	switch req.Op {
	case svcproto.OpStatus:
		return s.status()
	case svcproto.OpStart:
		engine := req.Engine
		if engine == "" {
			engine = svcproto.EngineSingBox
		}
		if s.cores[engine] == nil {
			return fail(fmt.Errorf("движок %q не поставляется в этой установке", engine))
		}
		if err := s.writeConfig(engine, req.Config, req.ConfigHash); err != nil {
			// A newer GUI may need a newer sanitiser, and this service may be
			// the old one still waiting for the core to stop. The tunnel is
			// not worth more than its way back: hand over now.
			go s.updates.settle(true)
			return fail(err)
		}
		awaited := s.awaitingConfig()
		if err := s.startCore(engine); err != nil {
			return fail(err)
		}
		if awaited {
			s.log.Logf("service: %s is up on the config this release sent (hash=%s, service %s)", engine, shortHash(req.ConfigHash), Version)
		}
		return s.status()
	case svcproto.OpResetNetwork:
		if s.cfg.NetworkStack == nil {
			return fail(errors.New("сброс сетевого стека не поддерживается на этой системе"))
		}
		return s.resetNetwork(req.Force, fail)
	case svcproto.OpScanNetwork:
		if s.cfg.NetworkStack == nil {
			return fail(errors.New("проверка системы не поддерживается на этой системе"))
		}
		scan, err := s.cfg.NetworkStack.Scan()
		if err != nil {
			return fail(err)
		}
		s.log.Logf("service: network scan: hosts overrides: %d, dpi tools: %s, foreign wfp: %s, windivert: %s",
			scan.HostsOverrides, svcproto.ScanList(scan.DPITools), svcproto.ScanList(scan.ForeignWFP), scan.WinDivert.LogValue())
		resp := s.status()
		resp.NetworkScan = &scan
		return resp
	case svcproto.OpAutoTunnel:
		// The host list arrives as data and is validated here, at the boundary:
		// the rule set file it becomes is the one the sanitised config names,
		// and the core reads it as SYSTEM/root.
		if err := autotunnel.ValidateHosts(req.Hosts); err != nil {
			return fail(fmt.Errorf("autotunnel: %w", err))
		}
		if err := autotunnel.WriteRuleSet(s.autoTunnelPath(), req.Hosts); err != nil {
			return fail(fmt.Errorf("autotunnel: %w", err))
		}
		s.log.Logf("service: autotunnel list: %d host(s)", len(req.Hosts))
		return s.status()
	case svcproto.OpRepairNetwork:
		if s.cfg.HostRepair == nil {
			return fail(errors.New("исправление сети службой не поддерживается на этой системе"))
		}
		repair, err := s.cfg.HostRepair.Repair(req.ProxyOnly)
		if err != nil {
			return fail(err)
		}
		// Details can name a proxy endpoint; the log keeps only the outcome.
		for _, a := range repair.Actions {
			s.log.Logf("service: network repair: %s done=%v", a.Name, a.Done)
		}
		resp := s.status()
		resp.NetworkRepair = &repair
		return resp
	case svcproto.OpStop:
		if req.Restarting {
			s.log.Logf("service: stop for a restart")
			if err := s.stopCores(); err != nil {
				return fail(err)
			}
			return s.status()
		}
		s.log.Logf("service: stop")
		if err := s.stopCore(); err != nil {
			return fail(err)
		}
		return s.status()
	default:
		return fail(fmt.Errorf("unknown op %q", req.Op))
	}
}

// resetNetwork refuses, changing nothing, while adapters with a static
// address would lose it and the user has not confirmed that (force). Then
// it stops the core — the reset rebuilds the stack the TUN sits on — and
// resets. The run marker goes with the core: after the reset the user comes
// back to a working network, not a tunnel that starts before they looked at
// it. Callers hold mu.
func (s *Server) resetNetwork(force bool, fail func(error) svcproto.Response) svcproto.Response {
	stack := s.cfg.NetworkStack
	static, err := stack.StaticAdapters()
	if err != nil {
		return fail(fmt.Errorf("проверка сетевых адаптеров: %w", err))
	}
	s.log.Logf("service: network reset requested (force=%v, static adapters: %d)", force, len(static))
	if len(static) > 0 && !force {
		resp := s.status()
		resp.NetworkReset = &svcproto.NetworkReset{Refused: true, StaticAdapters: static}
		return resp
	}
	if err := s.stopCore(); err != nil {
		return fail(fmt.Errorf("остановка ядра перед сбросом сети: %w", err))
	}
	res, err := stack.Reset()
	if res.Output != "" {
		s.log.Logf("service: network reset output:\n%s", res.Output)
	}
	if err != nil {
		return fail(err)
	}
	s.log.Logf("service: network reset done (hosts entries disabled: %d, dpi stopped: %v, dpi failed: %v, reboot required: %v)",
		res.HostsDisabled, res.DPIStopped, res.DPIFailed, res.RebootRequired)
	res.StaticAdapters = static
	resp := s.status()
	resp.NetworkReset = &res
	return resp
}

func (s *Server) onVersion(v string) {
	if s.cfg.OnVersion != nil {
		s.cfg.OnVersion(v)
	}
}

// configPath is where the service keeps its sanitised copy of an engine's
// config.
func (s *Server) configPath(engine string) string {
	if engine == svcproto.EngineXray {
		return filepath.Join(s.cfg.CoreDir, "xray.json")
	}
	return filepath.Join(s.cfg.CoreDir, "config.json")
}

// writeConfig sanitises a config from the GUI for engine and stores it
// where that engine's core reads it, with the GUI's fingerprint of it. Callers
// hold mu.
func (s *Server) writeConfig(engine string, raw []byte, hash string) error {
	var cfg []byte
	var err error
	if engine == svcproto.EngineXray {
		var dns string
		cfg, dns, err = svcproto.SanitizeXray(raw)
		if err == nil {
			err = writeAtomic(s.dnsMarker(), []byte(dns))
		}
	} else {
		cfg, err = svcproto.Sanitize(raw, svcproto.Paths{
			CacheFile:         filepath.Join(s.cfg.CoreDir, "cache.db"),
			AutoTunnelRuleSet: s.autoTunnelPath(),
		})
	}
	if err != nil {
		return fmt.Errorf("конфиг отклонён службой: %w", err)
	}
	if len(hash) > maxConfigHash {
		return fmt.Errorf("конфиг отклонён службой: отпечаток длиной %d", len(hash))
	}
	s.log.Logf("service: %s config (%d bytes) hash=%s", engine, len(cfg), shortHash(hash))
	if err := writeAtomic(s.configPath(engine), cfg); err != nil {
		return err
	}
	if err := writeAtomic(s.configHashPath(engine), []byte(hash)); err != nil {
		return err
	}
	return writeAtomic(s.configVersionPath(engine), []byte(Version))
}

// maxConfigHash bounds what is stored for a fingerprint the service cannot check.
const maxConfigHash = 128

// shortHash is a fingerprint for the log.
func shortHash(hash string) string {
	if hash == "" {
		return "none"
	}
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}

// configVersionPath records which version of the service received the engine's
// config (svcproto.Response.ConfigVersion).
func (s *Server) configVersionPath(engine string) string {
	return s.configPath(engine) + ".version"
}

// configHashPath holds the GUI's fingerprint of the stored config
// (svcproto.Response.ConfigHash).
func (s *Server) configHashPath(engine string) string {
	return s.configPath(engine) + ".hash"
}

// configFiles are what describes one engine's stored config.
func (s *Server) configFiles(engine string) []string {
	return []string{s.configPath(engine), s.configHashPath(engine), s.configVersionPath(engine)}
}

// keepConfigForRollback copies the stored configs aside before a swap. The new
// GUI sends its config while its version is on trial, and a rollback must not leave
// the previous core with rules of a release it never knew: it may refuse to
// start on them. Callers hold mu.
func (s *Server) keepConfigForRollback() {
	for engine := range s.cores {
		for _, path := range s.configFiles(engine) {
			raw, err := os.ReadFile(path)
			if err == nil {
				err = writeAtomic(path+rollbackSuffix, raw)
			} else {
				err = os.Remove(path + rollbackSuffix)
			}
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				s.log.Logf("service: keeping the config for a rollback: %v", err)
			}
		}
	}
}

// restoreConfigFromRollback puts back what keepConfigForRollback kept. Callers
// hold mu.
func (s *Server) restoreConfigFromRollback() {
	for engine := range s.cores {
		for _, path := range s.configFiles(engine) {
			if _, err := os.Stat(path + rollbackSuffix); err != nil {
				continue
			}
			if err := fsutil.Replace(path+rollbackSuffix, path); err != nil {
				s.log.Logf("service: restoring the config after a rollback: %v", err)
			}
		}
	}
}

// dropConfigForRollback forgets it once the version is confirmed.
func (s *Server) dropConfigForRollback() {
	for engine := range s.cores {
		for _, path := range s.configFiles(engine) {
			_ = os.Remove(path + rollbackSuffix)
		}
	}
}

const rollbackSuffix = ".rollback"

// awaitMarker exists while a wanted core stays down for the new release's config.
// It holds the deadline, unix seconds, and survives the service being replaced.
func (s *Server) awaitMarker() string { return filepath.Join(s.cfg.CoreDir, "await-config") }

// beginAwaitingConfig leaves the core down for the new GUI (see awaitConfigFor).
func (s *Server) beginAwaitingConfig() {
	deadline := time.Now().Add(awaitConfigFor).Unix()
	if err := writeAtomic(s.awaitMarker(), []byte(strconv.FormatInt(deadline, 10))); err != nil {
		s.log.Logf("service: marking the wait for a config: %v", err)
	}
}

func (s *Server) awaitingConfig() bool {
	_, err := os.Stat(s.awaitMarker())
	return err == nil
}

// awaitExpired: the wait is over, or its deadline makes no sense (the clock moved).
func (s *Server) awaitExpired(now time.Time) bool {
	raw, err := os.ReadFile(s.awaitMarker())
	if err != nil {
		return false
	}
	deadline, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil {
		return true
	}
	at := time.Unix(deadline, 0)
	return !now.Before(at) || at.After(now.Add(awaitConfigFor))
}

func (s *Server) endAwaitingConfig() { _ = os.Remove(s.awaitMarker()) }

// watchAwaitedConfig brings the core up on the stored config when the GUI never
// sent a new one. The tunnel the user wanted beats the rules of the new release; a
// GUI that arrives later finds the difference and reconnects.
func (s *Server) watchAwaitedConfig(ctx context.Context) {
	tick := time.NewTicker(coreExitPoll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		s.mu.Lock()
		s.startOnStoredConfigIfAwaitExpired(time.Now())
		s.mu.Unlock()
	}
}

// startOnStoredConfigIfAwaitExpired is one check of watchAwaitedConfig. Callers
// hold mu.
func (s *Server) startOnStoredConfigIfAwaitExpired(now time.Time) {
	if !s.awaitExpired(now) {
		return
	}
	s.endAwaitingConfig()
	if engine, _ := s.running(); engine != "" || !s.wanted() {
		return
	}
	s.log.Logf("service: no config from the app within %s, starting %s on the stored one", awaitConfigFor, s.engine)
	if err := s.runCore(s.engine); err != nil {
		s.log.Logf("service: start on the stored config failed: %v", err)
	}
}

// runMarker exists while the GUI wants the core up: what ResumeCore reads.
// It holds the engine.
func (s *Server) runMarker() string { return filepath.Join(s.cfg.CoreDir, "run") }

// dnsMarker holds the resolver address the stored Xray config answers on.
func (s *Server) dnsMarker() string { return filepath.Join(s.cfg.CoreDir, "xray.dns") }

// autoTunnelPath is the autotunnel rule set the core reads. The service owns
// it: the GUI pushes the hosts over the control endpoint, Sanitize points the
// config's rule set here, and the core watches the file and reloads it.
func (s *Server) autoTunnelPath() string { return filepath.Join(s.cfg.CoreDir, "auto-tunnel.json") }

// startCore and stopCore are the GUI's start and stop: they also record the
// wish, which outlives this process. Callers hold mu.
func (s *Server) startCore(engine string) error {
	if err := s.runCore(engine); err != nil {
		return err
	}
	s.endAwaitingConfig()
	_ = os.WriteFile(s.runMarker(), []byte(engine), 0o644)
	return nil
}

func (s *Server) stopCore() error {
	_ = os.Remove(s.runMarker())
	s.endAwaitingConfig()
	return s.stopCores()
}

// runCore starts engine's core on its stored config, stopping the other
// one first. The system resolver is pointed at the core that starts. Callers
// hold mu.
func (s *Server) runCore(engine string) error {
	for name, core := range s.cores {
		if name != engine && core.Pid() != 0 {
			s.log.Logf("service: stopping %s for %s", name, engine)
			_ = core.Stop()
		}
	}
	s.engine = engine
	if engine == svcproto.EngineXray {
		addr, err := os.ReadFile(s.dnsMarker())
		if err != nil {
			return fmt.Errorf("Xray: нет адреса DNS для конфига: %w", err)
		}
		if err := s.cfg.SystemDNS.Override(string(addr)); err != nil {
			_ = s.cfg.SystemDNS.Restore()
			return fmt.Errorf("Xray: не удалось настроить системный DNS: %w", err)
		}
	} else if s.cfg.SystemDNS != nil {
		if err := s.cfg.SystemDNS.Override(svcproto.SingBoxDNS); err != nil {
			_ = s.cfg.SystemDNS.Restore()
			return fmt.Errorf("не удалось настроить системный DNS: %w", err)
		}
	}
	if engine == svcproto.EngineSingBox {
		// The core refuses to start over a missing autotunnel rule set, and
		// nothing else creates it: the file is service-owned, the GUI only
		// pushes its content. Covers the first start and a resume into a
		// directory an update replaced.
		if err := autotunnel.EnsureRuleSet(s.autoTunnelPath()); err != nil {
			return fmt.Errorf("autotunnel rule set: %w", err)
		}
	}
	if err := s.cores[engine].Start(); err != nil {
		if s.cfg.SystemDNS != nil {
			_ = s.cfg.SystemDNS.Restore()
		}
		return err
	}
	return nil
}

// watchCoreExit gives the system resolver back when the core dies on its own:
// with the TUN gone the override points nowhere and the whole machine stops
// resolving, the direct path included. The next start sets it again.
func (s *Server) watchCoreExit(ctx context.Context) {
	if s.cfg.SystemDNS == nil {
		return
	}
	tick := time.NewTicker(coreExitPoll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		s.mu.Lock()
		s.releaseDeadCoreDNS()
		s.mu.Unlock()
	}
}

// releaseDeadCoreDNS is one check of watchCoreExit. Callers hold mu.
func (s *Server) releaseDeadCoreDNS() {
	for _, core := range s.cores {
		if core.Pid() != 0 {
			return
		}
	}
	if err := s.cfg.SystemDNS.Restore(); err != nil {
		s.log.Logf("service: restoring system DNS after the core exited: %v", err)
	}
}

// stopCores takes every core down and gives the system resolver back.
// Callers hold mu.
func (s *Server) stopCores() error {
	var first error
	for _, core := range s.cores {
		if err := core.Stop(); err != nil && first == nil {
			first = err
		}
	}
	if s.cfg.SystemDNS != nil {
		if err := s.cfg.SystemDNS.Restore(); err != nil && first == nil {
			first = fmt.Errorf("не удалось вернуть системный DNS: %w", err)
		}
	}
	return first
}

// running is the engine whose core is up, or "".
func (s *Server) running() (string, int) {
	for name, core := range s.cores {
		if pid := core.Pid(); pid != 0 {
			return name, pid
		}
	}
	return "", 0
}

// resume brings the core back if the previous service left it wanted up.
func (s *Server) resume() {
	raw, err := os.ReadFile(s.runMarker())
	if err != nil {
		return
	}
	engine := string(raw)
	if engine == "" {
		// A marker from before Xray: sing-box.
		engine = svcproto.EngineSingBox
	}
	if _, err := os.Stat(s.configPath(engine)); err != nil || s.cores[engine] == nil {
		_ = os.Remove(s.runMarker())
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.engine = engine
	if s.awaitingConfig() && !s.awaitExpired(time.Now()) {
		s.log.Logf("service: %s stays down until the app sends this release's config", engine)
		return
	}
	s.endAwaitingConfig()
	s.log.Logf("service: resuming %s (it was up before the restart)", engine)
	if err := s.runCore(engine); err != nil {
		s.log.Logf("service: resume failed: %v", err)
	}
}

func (s *Server) status() svcproto.Response {
	engine, pid := s.running()
	configVersion, configHash := "", ""
	if engine != "" {
		// Absent: a config from before this was recorded, so an older release.
		if raw, err := os.ReadFile(s.configVersionPath(engine)); err == nil {
			configVersion = string(raw)
		}
		if raw, err := os.ReadFile(s.configHashPath(engine)); err == nil {
			configHash = string(raw)
		}
	}
	return svcproto.Response{
		OK:      true,
		Running: pid != 0,
		Pid:     pid,
		Engine:  engine,
		CoreLog: s.log.Path(),
		Version: Version,
		Update:  s.updates.snapshot(),

		ConfigTracked: true,
		ConfigVersion: configVersion,
		Supervised:    true,
		Wanted:        s.wanted(),
		ConfigHash:    configHash,

		SyncsBinaries: s.cfg.SyncBinaries != nil,
		ScanSafe:      s.cfg.NetworkStack != nil,
	}
}

func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := fsutil.Replace(tmp, path); err != nil {
		return fmt.Errorf("placing %s: %w", path, err)
	}
	return nil
}
