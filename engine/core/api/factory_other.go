//go:build !windows && !darwin

package api

import (
	"fmt"
	"path/filepath"

	"NekkoVPN/core/engine"

	vpn "NekkoVPN/core/engine/singbox/process"
)

// newDesktopVPN: no service on this platform; the process manager here is a
// stub that fails loudly (see vpn.ProcessManager).
func newDesktopVPN(baseDir string, log vpn.LineLogger) vpn.Manager {
	return vpn.NewProcessManager(
		filepath.Join(baseDir, vpn.BinaryName),
		filepath.Join(baseDir, "config.json"),
		baseDir,
		log,
	)
}

// newDesktopXray: Xray needs a privileged host for TUN, and only the
// Hikkari service on Windows and macOS is one.
func newDesktopXray(string, engine.Logger) (engine.Backend, error) {
	return nil, fmt.Errorf("Xray на этой платформе не поддержан")
}
