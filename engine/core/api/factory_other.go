//go:build !windows

package api

import (
	"path/filepath"

	"NekkoVPN/core/vpn"
)

// newDesktopVPN: the engine manages the core itself (on macOS through its
// LaunchDaemon).
func newDesktopVPN(baseDir string, log vpn.LineLogger) vpn.Manager {
	return vpn.NewProcessManager(
		filepath.Join(baseDir, vpn.BinaryName),
		filepath.Join(baseDir, "config.json"),
		baseDir,
		log,
	)
}
