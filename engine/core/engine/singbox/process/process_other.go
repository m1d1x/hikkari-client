//go:build !windows && !darwin

package vpn

import "fmt"

// ProcessManager on platforms without a native backend (Windows has one,
// macOS has one) is an intentional stub. The mobile builds will swap this
// out for a libbox-backed Manager; for now any call returns
// an error so a misconfigured build fails loudly instead of silently
// pretending to work.
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
func (m *ProcessManager) Pid() int       { return 0 }
