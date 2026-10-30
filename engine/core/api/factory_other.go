//go:build !windows && !darwin

package api

import (
	"path/filepath"

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
