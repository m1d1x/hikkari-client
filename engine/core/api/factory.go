package api

// This is the composition root. It is the only application file that knows
// which concrete adapter is bundled; all services depend on engine.Backend.
import (
	"NekkoVPN/core/engine"
	"NekkoVPN/core/engine/singbox"
	vpn "NekkoVPN/core/engine/singbox/process"
	"NekkoVPN/core/engine/xray"
	"fmt"
)

// DefaultEngineFactory is sing-box driven the platform's way: through
// HikkariService on Windows and macOS (the GUI runs unelevated and must not
// start the core itself), a local process elsewhere (factory_*.go).
var DefaultEngineFactory engine.Factory = func(baseDir string, log engine.Logger) (engine.Backend, error) {
	return singbox.NewWithProcess(baseDir, log, newDesktopVPN(baseDir, log)), nil
}

// VPNFactory and InitWithVPN preserve the previous platform injection API.
// New integrations should inject a full adapter with InitWithEngine instead.
type VPNFactory func(string, vpn.LineLogger) vpn.Manager

// DefaultVPNFactory is the desktop factory: a service client on Windows, a
// ProcessManager elsewhere (see factory_*.go).
func DefaultVPNFactory(baseDir string, log vpn.LineLogger) vpn.Manager {
	return newDesktopVPN(baseDir, log)
}
func (c *Core) InitWithVPN(baseDir string, factory VPNFactory) error {
	if factory == nil {
		factory = DefaultVPNFactory
	}
	return c.InitWithEngine(baseDir, func(dir string, log engine.Logger) (engine.Backend, error) {
		return singbox.NewWithProcess(dir, log, factory(dir, log)), nil
	})
}

// InitForEngine selects the concrete chain only at the composition root.
func (c *Core) InitForEngine(baseDir, id string) error {
	c.mu.Lock()
	if c.ctx != nil && id != "" && c.backend.Info().ID != id {
		c.mu.Unlock()
		return fmt.Errorf("смена движка требует Shutdown перед повторным Init")
	}
	c.mu.Unlock()
	switch id {
	case "", "sing-box":
		return c.Init(baseDir)
	case "xray":
		return c.InitWithEngine(baseDir, xray.New)
	default:
		return fmt.Errorf("неизвестный движок %q", id)
	}
}
