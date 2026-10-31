//go:build windows || darwin

package api

import (
	"path/filepath"

	"NekkoVPN/core/engine"
	vpn "NekkoVPN/core/engine/singbox/process"
	"NekkoVPN/core/engine/xray"
)

// newDesktopVPN: the core is owned by HikkariService (LocalSystem on Windows,
// root on macOS) and the GUI only talks to it, which is what lets the GUI run
// unelevated.
func newDesktopVPN(baseDir string, log vpn.LineLogger) vpn.Manager {
	return vpn.NewServiceManager(filepath.Join(baseDir, "config.json"), log)
}

// newDesktopXray: as with sing-box, the Xray core is run by HikkariService;
// the GUI compiles and validates the config and hands it over.
func newDesktopXray(baseDir string, log engine.Logger) (engine.Backend, error) {
	return xray.New(baseDir, log, xray.ServiceHost)
}
