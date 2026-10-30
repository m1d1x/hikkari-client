//go:build windows || darwin

package api

import (
	"path/filepath"

	vpn "NekkoVPN/core/engine/singbox/process"
)

// newDesktopVPN: the core is owned by HikkariService (LocalSystem on Windows,
// root on macOS) and the GUI only talks to it, which is what lets the GUI run
// unelevated.
func newDesktopVPN(baseDir string, log vpn.LineLogger) vpn.Manager {
	return vpn.NewServiceManager(filepath.Join(baseDir, "config.json"), log)
}
