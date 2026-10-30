//go:build darwin

package vpn

import (
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// ProcessManager runs sing-box as a child of the macOS service
// (HikkariService, a root LaunchDaemon — see core/service).
//
// The core needs root for the utun device and routes; the service has it and
// the GUI does not. launchd owns the service — restarts it on crash, starts it
// at boot — and kills the service's process group when the service goes, so a
// core cannot outlive the process that answers for it.
type ProcessManager struct {
	binPath    string
	configPath string
	workDir    string
	log        LineLogger

	mu   sync.Mutex
	cmd  *exec.Cmd
	done chan struct{}
}

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
	flushDNS()

	cmd := exec.Command(m.binPath, "run", "-c", m.configPath)
	cmd.Dir = m.workDir
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		m.log.Logf("vpn.Start: failed to start sing-box: %v", err)
		return fmt.Errorf("не удалось запустить sing-box: %w", err)
	}
	m.cmd = cmd
	m.done = make(chan struct{})
	m.log.Logf("vpn.Start: sing-box started, pid=%d", cmd.Process.Pid)
	if stdout != nil {
		go m.log.PipeLines("singbox.out", stdout)
	}
	if stderr != nil {
		go m.log.PipeLines("singbox.err", stderr)
	}
	go func(c *exec.Cmd, done chan struct{}) {
		err := c.Wait()
		m.log.Logf("sing-box exited: err=%v, state=%v", err, c.ProcessState)
		close(done)
	}(cmd, m.done)
	return nil
}

// Stop asks the core to exit (it tears its routes down on SIGTERM) and kills
// it if it has not within a few seconds.
func (m *ProcessManager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.isAlive() {
		pid := m.cmd.Process.Pid
		m.log.Logf("vpn.Stop: stopping sing-box pid=%d", pid)
		_ = m.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-m.done:
		case <-time.After(5 * time.Second):
			m.log.Logf("vpn.Stop: pid=%d ignored SIGTERM, killing", pid)
			_ = m.cmd.Process.Kill()
			<-m.done
		}
	}
	m.cmd = nil
	flushDNS()
	m.log.Logf("vpn.Stop: done")
	return nil
}

func (m *ProcessManager) Restart() error {
	m.log.Logf("vpn.Restart: begin")
	if err := m.Stop(); err != nil {
		return err
	}
	time.Sleep(time.Second)
	err := m.Start()
	m.log.Logf("vpn.Restart: end, err=%v", err)
	return err
}

func (m *ProcessManager) Pid() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.isAlive() {
		return 0
	}
	return m.cmd.Process.Pid
}

func (m *ProcessManager) isAlive() bool {
	if m.cmd == nil || m.done == nil {
		return false
	}
	select {
	case <-m.done:
		return false
	default:
		return true
	}
}

// flushDNS drops answers cached while the tunnel was in a different state.
func flushDNS() {
	_ = exec.Command("dscacheutil", "-flushcache").Run()
	_ = exec.Command("killall", "-HUP", "mDNSResponder").Run()
}

func FlushDNS() { flushDNS() }
