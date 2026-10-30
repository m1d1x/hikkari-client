//go:build windows

// Package winsvc is HikkariService.exe: the privileged half of the Windows
// client. It owns the sing-box core (TUN needs administrator rights, the GUI
// no longer has them) and, later, applies updates.
//
//	<root>\                        Program Files\Hikkari — admin-only writable
//	    Hikkari.exe                launcher — the shortcut points here
//	    HikkariService.exe         this service, LocalSystem
//	    hikkari-updater.exe
//	    app\                       GUI, engine DLL, sing-box.exe, wintun.dll
//	    core\                      service-owned: config.json, cache.db, service.log
//
// Everything the service executes or writes lives under <root>, which only
// administrators can modify. The GUI reaches it through one pipe and sends
// config CONTENT, never a path (see svcproto.Sanitize for why).
package winsvc

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Microsoft/go-winio"

	"NekkoVPN/core/logging"
	"NekkoVPN/core/svcproto"
	"NekkoVPN/core/vpn"
)

// pipeSDDL: SYSTEM and Administrators full control, interactively logged-on
// users read/write (that is the GUI), network logons denied outright.
const pipeSDDL = "D:P(D;;GA;;;NU)(A;;GA;;;SY)(A;;GA;;;BA)(A;;GRGW;;;IU)"

// Version is stamped at build time (-ldflags -X).
var Version = "dev"

type Server struct {
	root    string
	coreDir string
	log     *logging.Logger
	core    *vpn.ProcessManager
	updates *updates

	// Guards the core: start/stop must not interleave. Update operations
	// that stop the core take it inside their own lock (see updates).
	mu sync.Mutex
}

// NewServer prepares the service's own directory and the core manager.
func NewServer(root string) (*Server, error) {
	coreDir := filepath.Join(root, "core")
	if err := os.MkdirAll(coreDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating %s: %w", coreDir, err)
	}
	log := logging.New(filepath.Join(coreDir, "service.log"))
	appDir := filepath.Join(root, "app")
	log.Logf("=== HikkariService %s, root=%s ===", Version, root)
	s := &Server{
		root:    root,
		coreDir: coreDir,
		log:     log,
		// The core runs from app\ (wintun.dll and libcronet.dll sit beside
		// it), with its config and cache in core\. app\ is replaced whole
		// by an update, which is fine: the core is stopped before that.
		core: vpn.NewProcessManager(
			filepath.Join(appDir, vpn.BinaryName),
			filepath.Join(coreDir, "config.json"),
			appDir,
			log,
		),
	}
	s.updates = newUpdates(s)
	return s, nil
}

// Serve answers the control pipe until ctx is cancelled, then stops the core.
func (s *Server) Serve(ctx context.Context) error {
	l, err := winio.ListenPipe(svcproto.PipeName, &winio.PipeConfig{
		SecurityDescriptor: pipeSDDL,
		InputBufferSize:    64 << 10,
		OutputBufferSize:   64 << 10,
	})
	if err != nil {
		return fmt.Errorf("listening on %s: %w", svcproto.PipeName, err)
	}
	s.log.Logf("service: listening on %s", svcproto.PipeName)
	go s.updates.loop(ctx)
	go func() {
		<-ctx.Done()
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
	s.log.Logf("service: stopping, taking the core down")
	s.mu.Lock()
	_ = s.core.Stop()
	s.mu.Unlock()
	return s.log.Close()
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
	_ = svcproto.WriteMessage(conn, s.dispatch(req))
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
			CacheFile: filepath.Join(s.coreDir, "cache.db"),
		})
		if err != nil {
			return fail(fmt.Errorf("конфиг отклонён службой: %w", err))
		}
		if err := writeAtomic(filepath.Join(s.coreDir, "config.json"), cfg); err != nil {
			return fail(err)
		}
		s.log.Logf("service: start (config %d bytes)", len(cfg))
		if err := s.core.Start(); err != nil {
			return fail(err)
		}
		return s.status()
	case svcproto.OpStop:
		s.log.Logf("service: stop")
		if err := s.core.Stop(); err != nil {
			return fail(err)
		}
		return s.status()
	default:
		return fail(fmt.Errorf("unknown op %q", req.Op))
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
