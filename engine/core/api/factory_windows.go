//go:build windows

package api

import (
	"path/filepath"

	vpn "NekkoVPN/core/engine/singbox/process"
)

// newDesktopVPN: on Windows the core is owned by HikkariService (LocalSystem)
// and the GUI only talks to it, which is what lets the GUI run unelevated.
func newDesktopVPN(baseDir string, log vpn.LineLogger) vpn.Manager {
	return vpn.NewServiceManager(filepath.Join(baseDir, "config.json"), log)
}
