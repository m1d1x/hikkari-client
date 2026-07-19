//go:build !windows && !darwin

package vpn

import "fmt"

// ProcessManager on the remaining platforms (Linux, and the mobile GOOS
// targets) is an intentional stub. Windows has process_windows.go and macOS
// has process_darwin.go; the mobile builds will swap this out for a
// libbox-backed Manager. For now any call returns an error so a misconfigured
// build fails loudly instead of silently pretending to work.
type ProcessManager struct {
	log LineLogger
}

func NewProcessManager(_ string, _ string, _ string, log LineLogger) *ProcessManager {
	return &ProcessManager{log: log}
}

func (m *ProcessManager) Start() error {
	return fmt.Errorf("vpn.ProcessManager.Start: not implemented on this platform")
}

func (m *ProcessManager) Stop() error    { return nil }
func (m *ProcessManager) Restart() error { return m.Start() }
