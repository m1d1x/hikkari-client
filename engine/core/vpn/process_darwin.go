//go:build darwin

package vpn

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// ProcessManager runs the bundled `sing-box` binary as a child process. It is
// the macOS counterpart of the Windows implementation in process_windows.go
// and speaks the same Manager interface.
//
// macOS differs from Windows in three ways that shape this code:
//
//   - There is no Job Object, so we cannot ask the kernel to kill sing-box
//     when we die. Instead the child is put in its own process group
//     (Setpgid) so Stop() can signal every descendant at once, and both
//     Start() and Stop() run a `pkill` sweep to reap strays left behind by a
//     previous crash — the same belt-and-suspenders idea as the Windows
//     taskkill sweep. The one guarantee we lose is "sing-box dies with us on
//     an unclean crash"; a privileged helper (launchd) is the proper fix and
//     is left for later.
//
//   - sing-box's TUN inbound needs root to create the utun interface. We do
//     NOT elevate here — that's the host app's or a privileged helper's job.
//     If we aren't effectively root we log a loud warning so the failure
//     shows up as a clear line instead of a cryptic sing-box permission error.
//
//   - Stop() sends SIGTERM first so sing-box can tear the utun down and
//     restore the system routes and DNS, then falls back to SIGKILL. A hard
//     kill alone can leave the machine with no working network until reboot.
type ProcessManager struct {
	binPath    string
	configPath string
	workDir    string
	log        LineLogger

	mu   sync.Mutex
	cmd  *exec.Cmd
	done chan struct{} // closed by the wait goroutine when cmd exits
}

// NewProcessManager wires the manager to the sing-box binary and config it
// should run. workDir is the cwd for the child (typically the same folder as
// binPath).
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
	if os.Geteuid() != 0 {
		m.log.Logf("vpn.Start: warning, not running as root (euid=%d) — sing-box "+
			"cannot create the utun device; launch the app elevated or install a "+
			"privileged helper", os.Geteuid())
	}
	// Reap any sing-box left over from a previous crash before we try to grab
	// the utun device ourselves.
	m.killStrays()

	cmd := exec.Command(m.binPath, "run", "-c", m.configPath)
	cmd.Dir = m.workDir
	// Own process group so Stop() can signal the whole tree at once.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		m.log.Logf("vpn.Start: failed to start sing-box: %v", err)
		return fmt.Errorf("не удалось запустить sing-box: %w", err)
	}
	m.cmd = cmd
	done := make(chan struct{})
	m.done = done
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
		close(done)
	}(cmd)
	return nil
}

func (m *ProcessManager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd != nil && m.cmd.Process != nil {
		pid := m.cmd.Process.Pid
		done := m.done
		m.log.Logf("vpn.Stop: terminating sing-box pid=%d (SIGTERM)", pid)
		m.signalGroup(pid, syscall.SIGTERM)
		// Give sing-box a moment to unwind the utun and restore routing/DNS.
		if !waitClosed(done, 3*time.Second) {
			m.log.Logf("vpn.Stop: no exit on SIGTERM, escalating to SIGKILL")
			m.signalGroup(pid, syscall.SIGKILL)
			waitClosed(done, 2*time.Second)
		}
		m.cmd = nil
		m.done = nil
	}
	m.killStrays()
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

// signalGroup sends sig to the child's whole process group (negative pid).
// sing-box is started with Setpgid, so its pgid equals its pid. If the group
// has already gone we fall back to signalling the bare pid.
func (m *ProcessManager) signalGroup(pid int, sig syscall.Signal) {
	if err := syscall.Kill(-pid, sig); err != nil {
		_ = syscall.Kill(pid, sig)
	}
}

// killStrays runs `pkill -x sing-box`. Safe to call when there is nothing to
// kill — pkill exits non-zero in that case and we ignore it. Like the Windows
// taskkill sweep, this may also catch a sing-box a user launched by hand; that
// is the intended paranoia against double-grabbing the utun device.
func (m *ProcessManager) killStrays() {
	_ = exec.Command("pkill", "-x", "sing-box").Run()
}

func (m *ProcessManager) isAlive() bool {
	if m.cmd == nil || m.cmd.Process == nil || m.done == nil {
		return false
	}
	select {
	case <-m.done:
		return false
	default:
		return true
	}
}

// waitClosed blocks until ch is closed or the timeout elapses. Returns true if
// the channel closed (process exited) in time.
func waitClosed(ch chan struct{}, timeout time.Duration) bool {
	if ch == nil {
		return true
	}
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case <-ch:
		return true
	case <-t.C:
		return false
	}
}
