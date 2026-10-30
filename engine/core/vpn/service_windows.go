//go:build windows

package vpn

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"NekkoVPN/core/svcclient"
	"NekkoVPN/core/svcproto"
)

// ServiceManager is the GUI's handle on the core when the core is owned by
// the Hikkari service (HikkariService.exe, running as LocalSystem).
//
// This is what lets the GUI run without administrator rights: bringing up
// TUN needs them, and the service already has them. The GUI generates the
// config as before and hands its CONTENT over the control pipe; the service
// sanitises it (svcproto.Sanitize), writes its own copy and runs the core.
// No path the GUI controls is ever executed or opened by the service.
//
// Consequence worth knowing, same as on macOS: the core lives in the service,
// so a GUI crash leaves the tunnel up instead of dropping traffic to the
// clear. A clean GUI shutdown stops it (api.Core.Shutdown → Stop).
type ServiceManager struct {
	configPath string
	log        LineLogger

	followOnce sync.Once
}

func NewServiceManager(configPath string, log LineLogger) *ServiceManager {
	return &ServiceManager{configPath: configPath, log: log}
}

func (m *ServiceManager) call(req svcproto.Request, timeout time.Duration) (svcproto.Response, error) {
	resp, err := svcclient.Call(req, timeout)
	if errors.Is(err, svcclient.ErrNoService) {
		m.log.Logf("vpn.service: %v", err)
		return resp, svcclient.ErrNoService
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
	m.followOnce.Do(func() { followFile(resp.CoreLog, "service", m.log) })
}

func (m *ServiceManager) Start() error {
	raw, err := os.ReadFile(m.configPath)
	if err != nil {
		return fmt.Errorf("конфиг sing-box не найден: %s", m.configPath)
	}
	if !json.Valid(raw) {
		return fmt.Errorf("конфиг sing-box повреждён: %s", m.configPath)
	}
	if st, err := m.call(svcproto.Request{Op: svcproto.OpStatus}, 5*time.Second); err == nil {
		m.followCore(st)
	}
	// Generous: the service stops strays, unblocks binaries and flushes DNS
	// before spawning the core.
	resp, err := m.call(svcproto.Request{Op: svcproto.OpStart, Config: raw}, 30*time.Second)
	if err != nil {
		m.log.Logf("vpn.Start (service): %v", err)
		return err
	}
	m.log.Logf("vpn.Start (service): core running, pid=%d", resp.Pid)
	return nil
}

func (m *ServiceManager) Stop() error {
	_, err := m.call(svcproto.Request{Op: svcproto.OpStop}, 30*time.Second)
	if err != nil {
		m.log.Logf("vpn.Stop (service): %v", err)
		return err
	}
	m.log.Logf("vpn.Stop (service): done")
	return nil
}

func (m *ServiceManager) Restart() error {
	m.log.Logf("vpn.Restart (service): begin")
	if err := m.Stop(); err != nil {
		return err
	}
	time.Sleep(1500 * time.Millisecond)
	err := m.Start()
	m.log.Logf("vpn.Restart (service): end, err=%v", err)
	return err
}
