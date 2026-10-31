//go:build windows || darwin

package xray

import (
	"context"
	"fmt"
	"os"
	"time"

	"NekkoVPN/core/svcclient"
	"NekkoVPN/core/svcproto"
)

// ServiceHost runs the core through the Hikkari service (core/service:
// LocalSystem on Windows, root on macOS), which is what lets the GUI run
// unelevated: TUN and system DNS need those rights and the service has them.
//
// The service is handed the config CONTENT, sanitises it
// (svcproto.SanitizeXray) and runs its own bundled Xray on its own copy.
// The binary the backend validated with is not passed on: the service never
// executes a path the GUI names.
func ServiceHost(_ string, config string, ports map[string]int) Host {
	return &serviceHost{config: config, ports: ports}
}

type serviceHost struct {
	config string
	ports  map[string]int
}

func (h *serviceHost) Start(ctx context.Context) error {
	raw, err := os.ReadFile(h.config)
	if err != nil {
		return fmt.Errorf("конфиг Xray не найден: %w", err)
	}
	// Generous: the service stops the other core and redirects system DNS
	// before spawning this one.
	if _, err := svcclient.Call(svcproto.Request{Op: svcproto.OpStart, Engine: svcproto.EngineXray, Config: raw}, 30*time.Second); err != nil {
		return err
	}
	deadline := time.Now().Add(20 * time.Second)
	for !listenersReady(h.ports) {
		if time.Now().After(deadline) {
			return fmt.Errorf("Xray не открыл локальные порты за 20 секунд; журнал службы — в логе движка")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return nil
}

func (h *serviceHost) Stop() error {
	_, err := svcclient.Call(svcproto.Request{Op: svcproto.OpStop}, 30*time.Second)
	return err
}

// IsRunning: the service runs Xray and Xray answers on this host's ports.
func (h *serviceHost) IsRunning() bool {
	st, err := svcclient.Call(svcproto.Request{Op: svcproto.OpStatus}, 5*time.Second)
	return err == nil && st.Running && st.Engine == svcproto.EngineXray && listenersReady(h.ports)
}
