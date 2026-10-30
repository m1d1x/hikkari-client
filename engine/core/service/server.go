// Package service is the privileged half of the desktop client: HikkariService
// on Windows (LocalSystem, core/winsvc registers it with the SCM) and on macOS
// (root, a LaunchDaemon, core/macsvc). It owns the sing-box core — TUN needs
// those rights and the GUI does not have them — and applies updates.
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

// ErrRestart: Serve returned because an update replaced the service.
var ErrRestart = errors.New("service: restarting into the updated version")

type Server struct {
	cfg     Config
	log     *logging.Logger
	core    *vpn.ProcessManager
	updates *updates

	// Guards the core: start/stop must not interleave. Update operations
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
	log := logging.New(filepath.Join(cfg.CoreDir, "service.log"))
	log.Logf("=== HikkariService %s, core=%s ===", Version, cfg.CoreBin)
	s := &Server{
		cfg:     cfg,
		log:     log,
		core:    vpn.NewProcessManager(cfg.CoreBin, filepath.Join(cfg.CoreDir, "config.json"), cfg.CoreWorkDir, log),
		restart: make(chan struct{}, 1),
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
		_ = s.core.Stop()
	} else {
		s.log.Logf("service: stopping, taking the core down")
		_ = s.core.Stop()
	}
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
		cfg, err := svcproto.Sanitize(req.Config, svcproto.Paths{
			CacheFile: filepath.Join(s.cfg.CoreDir, "cache.db"),
		})
		if err != nil {
			return fail(fmt.Errorf("конфиг отклонён службой: %w", err))
		}
		if err := writeAtomic(filepath.Join(s.cfg.CoreDir, "config.json"), cfg); err != nil {
			return fail(err)
		}
		s.log.Logf("service: start (config %d bytes)", len(cfg))
		if err := s.startCore(); err != nil {
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

// runMarker exists while the GUI wants the core up: what ResumeCore reads.
func (s *Server) runMarker() string { return filepath.Join(s.cfg.CoreDir, "run") }

// startCore and stopCore are the GUI's start and stop: they also record the
// wish, which outlives this process. Callers hold mu.
func (s *Server) startCore() error {
	if err := s.core.Start(); err != nil {
		return err
	}
	_ = os.WriteFile(s.runMarker(), nil, 0o644)
	return nil
}

func (s *Server) stopCore() error {
	_ = os.Remove(s.runMarker())
	return s.core.Stop()
}

// resume brings the core back if the previous service left it wanted up.
func (s *Server) resume() {
	if _, err := os.Stat(s.runMarker()); err != nil {
		return
	}
	if _, err := os.Stat(filepath.Join(s.cfg.CoreDir, "config.json")); err != nil {
		_ = os.Remove(s.runMarker())
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log.Logf("service: resuming the core (it was up before the restart)")
	if err := s.core.Start(); err != nil {
		s.log.Logf("service: resume failed: %v", err)
	}
}

func (s *Server) status() svcproto.Response {
	pid := s.core.Pid()
	return svcproto.Response{
		OK:      true,
		Running: pid != 0,
		Pid:     pid,
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
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("placing %s: %w", path, err)
	}
	return nil
}
