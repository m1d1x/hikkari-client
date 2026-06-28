//go:build windows

package vpn

import (
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

const createNoWindow = 0x08000000

// ProcessManager runs sing-box.exe as a hidden child process. Stop() also
// invokes `taskkill /F /IM sing-box.exe` as belt-and-suspenders cleanup for
// stray cores left by older builds (e.g. the legacy tray app).
type ProcessManager struct {
	binPath    string
	configPath string
	workDir    string
	log        LineLogger

	mu  sync.Mutex
	cmd *exec.Cmd
}

// NewProcessManager wires the manager to the sing-box binary and config it
// should run. workDir is the cwd for the child (typically the same folder
// as binPath, so sing-box can find wintun.dll alongside it).
func NewProcessManager(binPath, configPath, workDir string, log LineLogger) *ProcessManager {
	return &ProcessManager{
		binPath:    binPath,
		configPath: configPath,
		workDir:    workDir,
		log:        log,
	}
}

func (m *ProcessManager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.isAlive() {
		m.log.Logf("vpn.Start: core already alive (pid=%d), no-op", m.cmd.Process.Pid)
		return nil
	}
	cmd := exec.Command(m.binPath, "run", "-c", m.configPath)
	cmd.Dir = m.workDir
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}

	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		m.log.Logf("vpn.Start: failed to start sing-box: %v", err)
		return fmt.Errorf("не удалось запустить sing-box: %w", err)
	}
	m.cmd = cmd
	m.log.Logf("vpn.Start: sing-box started, pid=%d", cmd.Process.Pid)

	if stdout != nil {
		go m.log.PipeLines("singbox.out", stdout)
	}
	if stderr != nil {
		go m.log.PipeLines("singbox.err", stderr)
	}

	go func(c *exec.Cmd) {
		err := c.Wait()
		m.log.Logf("sing-box exited: err=%v, state=%v", err, c.ProcessState)
	}(cmd)
	return nil
}

func (m *ProcessManager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd != nil && m.cmd.Process != nil {
		m.log.Logf("vpn.Stop: killing sing-box pid=%d", m.cmd.Process.Pid)
		_ = m.cmd.Process.Kill()
		m.cmd = nil
	}
	kill := exec.Command("taskkill", "/F", "/IM", "sing-box.exe")
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	_ = kill.Run()
	m.log.Logf("vpn.Stop: done")
	return nil
}

func (m *ProcessManager) Restart() error {
	m.log.Logf("vpn.Restart: begin")
	if err := m.Stop(); err != nil {
		m.log.Logf("vpn.Restart: stop error: %v", err)
		return err
	}
	time.Sleep(1500 * time.Millisecond)
	err := m.Start()
	m.log.Logf("vpn.Restart: end, err=%v", err)
	return err
}

func (m *ProcessManager) isAlive() bool {
	return m.cmd != nil && m.cmd.Process != nil && m.cmd.ProcessState == nil
}
