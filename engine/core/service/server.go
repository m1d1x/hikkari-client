// Package service is the privileged half of the desktop client: HikkariService
// on Windows (LocalSystem, core/winsvc registers it with the SCM) and on macOS
// (root, a LaunchDaemon, core/macsvc). It owns the sing-box core — TUN needs
// those rights and the GUI does not have them — and applies updates. The core
// is sing-box or Xray, whichever engine the GUI runs; one at a time.
//
// The GUI reaches it through one local endpoint (a named pipe on Windows, a
// unix socket on macOS) and sends config CONTENT, never a path: the service
// runs the core with full rights, so every path in that config would be a
// path opened as SYSTEM/root (see svcproto.Sanitize).
//
// Everything platform-specific — where things are, the endpoint, how the
// updater is spawned, what an applied update means for the service itself —
// comes in through Config. What is here is the same on both platforms.
package service

import (
	"NekkoVPN/core/fsutil"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
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
	// SystemDNS points the system resolver at the running core while Xray
	// runs (sing-box takes DNS over by itself). Required with XrayBin.
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
	// after a swap the service exits (and the system starts the new one)
	// instead of bringing the core back itself. Serve then returns
	// ErrRestart. The core's wanted state survives in CoreDir/run and the
	// new service resumes it. (macOS: the service lives inside the bundle.
	// Windows: it lives outside app\ and stays.)
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
	// trial and no updater running. restart: the service's own binary was
	// replaced and the platform has arranged for the new one to start once
	// this one exits — Serve then returns ErrRestart. Optional.
	SyncBinaries func() (restart bool)
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

// ErrRestart: Serve returned because an update replaced the service.
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
	s := &Server{
		cfg: cfg,
		log: log,
		cores: map[string]*vpn.ProcessManager{
			svcproto.EngineSingBox: vpn.NewProcessManager(cfg.CoreBin, filepath.Join(cfg.CoreDir, "config.json"), cfg.CoreWorkDir, log),
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
		// A previous service that died with Xray up left the resolver
		// pointed at a core that is gone.
		if err := s.cfg.SystemDNS.Restore(); err != nil {
			s.log.Logf("service: restoring system DNS: %v", err)
		}
	}
	if s.cfg.ResumeCore {
		s.resume()
	}
	go s.updates.watchTrial(ctx)
	go s.updates.settle()

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
	case req.Op == svcproto.OpConfirmUpdate, req.Op == svcproto.OpRollbackUpdate:
		// The installed version is settled: bring the binaries outside
		// it in line — after the answer is out, since that may restart
		// the service.
		go s.updates.settle()
	}
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
		if err := s.writeConfig(engine, req.Config); err != nil {
			return fail(err)
		}
		if err := s.startCore(engine); err != nil {
			return fail(err)
		}
		return s.status()
	case svcproto.OpStop:
		s.log.Logf("service: stop")
		if err := s.stopCore(); err != nil {
			return fail(err)
		}
		return s.status()
	default:
		return fail(fmt.Errorf("unknown op %q", req.Op))
	}
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
// where that engine's core reads it. Callers hold mu.
func (s *Server) writeConfig(engine string, raw []byte) error {
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
			CacheFile: filepath.Join(s.cfg.CoreDir, "cache.db"),
		})
	}
	if err != nil {
		return fmt.Errorf("конфиг отклонён службой: %w", err)
	}
	s.log.Logf("service: %s config (%d bytes)", engine, len(cfg))
	return writeAtomic(s.configPath(engine), cfg)
}

// runMarker exists while the GUI wants the core up: what ResumeCore reads.
// It holds the engine.
func (s *Server) runMarker() string { return filepath.Join(s.cfg.CoreDir, "run") }

// dnsMarker holds the resolver address the stored Xray config answers on.
func (s *Server) dnsMarker() string { return filepath.Join(s.cfg.CoreDir, "xray.dns") }

// startCore and stopCore are the GUI's start and stop: they also record the
// wish, which outlives this process. Callers hold mu.
func (s *Server) startCore(engine string) error {
	if err := s.runCore(engine); err != nil {
		return err
	}
	_ = os.WriteFile(s.runMarker(), []byte(engine), 0o644)
	return nil
}

func (s *Server) stopCore() error {
	_ = os.Remove(s.runMarker())
	return s.stopCores()
}

// runCore starts engine's core on its stored config, stopping the other
// one first. Xray gets the system resolver pointed at it; sing-box hijacks
// DNS by itself. Callers hold mu.
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
		if err := s.cfg.SystemDNS.Restore(); err != nil {
			return fmt.Errorf("не удалось вернуть системный DNS: %w", err)
		}
	}
	if err := s.cores[engine].Start(); err != nil {
		if engine == svcproto.EngineXray {
			_ = s.cfg.SystemDNS.Restore()
		}
		return err
	}
	return nil
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
	s.log.Logf("service: resuming %s (it was up before the restart)", engine)
	if err := s.runCore(engine); err != nil {
		s.log.Logf("service: resume failed: %v", err)
	}
}

func (s *Server) status() svcproto.Response {
	engine, pid := s.running()
	return svcproto.Response{
		OK:      true,
		Running: pid != 0,
		Pid:     pid,
		Engine:  engine,
		CoreLog: s.log.Path(),
		Version: Version,
		Update:  s.updates.snapshot(),

		SyncsBinaries: s.cfg.SyncBinaries != nil,
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
