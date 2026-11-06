//go:build windows || darwin

package vpn

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"NekkoVPN/core/appversion"
	"NekkoVPN/core/engine"
	"NekkoVPN/core/svcclient"
	"NekkoVPN/core/svcproto"
)

// ServiceManager is the GUI's handle on the core when the core is owned by
// the hikkari service (core/service: LocalSystem on Windows, root on macOS).
//
// This is what lets the GUI run without administrator rights: bringing up
// TUN needs them, and the service already has them. The GUI generates the
// config as before and hands its CONTENT over the control endpoint; the service
// sanitises it (svcproto.Sanitize), writes its own copy and runs the core.
// No path the GUI controls is ever executed or opened by the service.
//
// Consequence worth knowing: the core lives in the service,
// so a GUI crash leaves the tunnel up instead of dropping traffic to the
// clear. A clean GUI shutdown stops it (api.Core.Shutdown → Stop).
type ServiceManager struct {
	configPath string
	log        LineLogger

	followOnce sync.Once
	following  atomic.Bool
	configHash atomic.Pointer[string]
}

// serviceReturnWithin is how long a start waits for a service that was there
// and is gone: it is restarting into updated binaries after a stop.
const serviceReturnWithin = 15 * time.Second

// attachInterval is how often a manager that has not reached the service yet
// asks for its status, so the core log is mirrored from the start even when
// the GUI attaches to a tunnel already running (a GUI crash, a core the service
// resumed after a reboot) and Start is never called.
const attachInterval = 10 * time.Second

func NewServiceManager(configPath string, log LineLogger) *ServiceManager {
	m := &ServiceManager{configPath: configPath, log: log}
	go m.attach()
	return m
}

func (m *ServiceManager) attach() {
	for !m.following.Load() {
		if _, err := m.call(svcproto.Request{Op: svcproto.OpStatus}, 5*time.Second); err == nil && m.following.Load() {
			return
		}
		time.Sleep(attachInterval)
	}
}

func (m *ServiceManager) call(req svcproto.Request, timeout time.Duration) (svcproto.Response, error) {
	resp, err := svcclient.Call(req, timeout)
	if errors.Is(err, svcclient.ErrNoService) {
		m.log.Logf("vpn.service: %v", err)
		return resp, svcclient.ErrNoService
	}
	if err == nil {
		m.followCore(resp)
	}
	return resp, err
}

// followCore mirrors the service's log (which carries the core's own
// stdout/stderr) into the engine log, so the one file a tester sends has
// both halves.
func (m *ServiceManager) followCore(resp svcproto.Response) {
	if resp.CoreLog == "" {
		return
	}
	m.followOnce.Do(func() {
		m.following.Store(true)
		offsetPath := filepath.Join(filepath.Dir(m.configPath), "service-log.offset")
		followFile(resp.CoreLog, offsetPath, "service", m.log)
	})
}

// SetConfigHash names the config the next Start sends.
func (m *ServiceManager) SetConfigHash(hash string) { m.configHash.Store(&hash) }

// CoreState reports what the service knows of the core. Whether it runs the config
// behind currentHash is by content: a release that changes no rule costs no
// reconnect, and one that does is seen whichever service version wrote the file.
func (m *ServiceManager) CoreState(currentHash string) (engine.CoreState, error) {
	resp, err := m.call(svcproto.Request{Op: svcproto.OpStatus}, 5*time.Second)
	if err != nil {
		return engine.CoreState{}, err
	}
	return engine.CoreState{
		Supervised:    resp.Supervised,
		Wanted:        resp.Wanted,
		Running:       resp.Running,
		ConfigCurrent: resp.ConfigHash == currentHash,
	}, nil
}

func (m *ServiceManager) Start() error {
	raw, err := os.ReadFile(m.configPath)
	if err != nil {
		return fmt.Errorf("конфиг sing-box не найден: %s", m.configPath)
	}
	if !json.Valid(raw) {
		return fmt.Errorf("конфиг sing-box повреждён: %s", m.configPath)
	}
	// The status answer starts the core log mirror (call → followCore).
	_, _ = m.call(svcproto.Request{Op: svcproto.OpStatus}, 5*time.Second)
	// Generous: the service stops strays, unblocks binaries and flushes DNS
	// before spawning the core.
	hash := ""
	if h := m.configHash.Load(); h != nil {
		hash = *h
	}
	request := svcproto.Request{Op: svcproto.OpStart, Config: raw, ConfigHash: hash, AppVersion: appversion.Own()}
	resp, err := m.call(request, 30*time.Second)
	for deadline := time.Now().Add(serviceReturnWithin); errors.Is(err, svcclient.ErrNoService) && m.following.Load() && time.Now().Before(deadline); {
		time.Sleep(time.Second)
		resp, err = m.call(request, 30*time.Second)
	}
	if err != nil {
		m.log.Logf("vpn.Start (service): %v", err)
		return err
	}
	m.log.Logf("vpn.Start (service): core running, pid=%d", resp.Pid)
	return nil
}

func (m *ServiceManager) Stop() error { return m.stop(false) }

// StopForRestart leaves the core wanted: the start that follows may fail.
func (m *ServiceManager) StopForRestart() error { return m.stop(true) }

func (m *ServiceManager) stop(restarting bool) error {
	_, err := m.call(svcproto.Request{Op: svcproto.OpStop, Restarting: restarting}, 30*time.Second)
	if errors.Is(err, svcclient.ErrNoService) {
		// The core belongs to the service: with no service there is no core
		// to stop. Start still reports the missing service to the user.
		m.log.Logf("vpn.Stop (service): no service, nothing to stop")
		return nil
	}
	if err != nil {
		m.log.Logf("vpn.Stop (service): %v", err)
		return err
	}
	m.log.Logf("vpn.Stop (service): done")
	return nil
}

func (m *ServiceManager) Restart() error {
	m.log.Logf("vpn.Restart (service): begin")
	if err := m.StopForRestart(); err != nil {
		return err
	}
	time.Sleep(1500 * time.Millisecond)
	err := m.Start()
	m.log.Logf("vpn.Restart (service): end, err=%v", err)
	return err
}
