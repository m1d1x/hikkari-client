//go:build windows

package vpn

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const createNoWindow = 0x08000000

// unblockBinary removes the NTFS Zone.Identifier stream (Mark of the Web)
// which triggers Windows Smart App Control ("An Application Control policy has blocked this file").
func unblockBinary(path string) {
	_ = os.Remove(path + ":Zone.Identifier")
	dir := filepath.Dir(path)
	_ = os.Remove(filepath.Join(dir, "wintun.dll:Zone.Identifier"))
	_ = os.Remove(filepath.Join(dir, "libcronet.dll:Zone.Identifier"))
	_ = os.Remove(filepath.Join(dir, "nekkobox.dll:Zone.Identifier"))

	psCmd := fmt.Sprintf(`Unblock-File -LiteralPath "%s"`, path)
	c := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", psCmd)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	_ = c.Run()
}

// ProcessManager runs sing-box.exe as a hidden child process. The child is
// wrapped in a Windows Job Object configured with
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE: when nekkobox.exe (us) dies for ANY
// reason — clean shutdown, segfault, force-kill, EGL-induced render crash —
// the kernel terminates sing-box along with us. This is what keeps orphan
// sing-box.exe processes from sticking around holding TUN devices, log
// files, and (in admin-launched builds) being unkillable from a regular
// user shell.
//
// Stop() additionally runs `taskkill /F /IM sing-box.exe` as belt-and-
// suspenders cleanup for stray cores from older pre-job builds, and
// Start() does the same sweep *before* spawning — paranoia against
// double-instance races for the TUN adapter.
type ProcessManager struct {
	binPath    string
	configPath string
	workDir    string
	log        LineLogger

	mu  sync.Mutex
	cmd *exec.Cmd
	// job is held for the lifetime of this manager (and therefore of the
	// host process). When the last handle closes — i.e. our process exits —
	// every process assigned to the job is terminated by the kernel.
	job windows.Handle
}

// NewProcessManager wires the manager to the sing-box binary and config it
// should run. workDir is the cwd for the child (typically the same folder
// as binPath, so sing-box can find wintun.dll alongside it).
func NewProcessManager(binPath, configPath, workDir string, log LineLogger) *ProcessManager {
	m := &ProcessManager{
		binPath:    binPath,
		configPath: configPath,
		workDir:    workDir,
		log:        log,
	}
	if h, err := createKillOnCloseJob(); err != nil {
		// Degrading gracefully: without the job object, we still have the
		// taskkill sweep in Stop() and Start(), so the worst-case is the
		// pre-PR behaviour (orphan possible on crash) — not a regression.
		m.log.Logf("vpn: warning, could not create Job Object (sing-box may orphan on crash): %v", err)
	} else {
		m.job = h
	}
	return m
}

// createKillOnCloseJob creates a Windows Job Object set to kill all
// assigned processes when its last handle is closed. We never call
// CloseHandle on the returned handle — letting the OS clean it up on
// process exit is the whole mechanism.
func createKillOnCloseJob() (windows.Handle, error) {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("CreateJobObject: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(
		h,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		windows.CloseHandle(h)
		return 0, fmt.Errorf("SetInformationJobObject: %w", err)
	}
	return h, nil
}

func (m *ProcessManager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.isAlive() {
		m.log.Logf("vpn.Start: core already alive (pid=%d), no-op", m.cmd.Process.Pid)
		return nil
	}
	// Catch any leftover sing-box from a previous crash. With the job
	// object below, our own future children die with us; this only fires
	// for orphans pre-dating that mechanism or for ones that somehow
	// escaped (e.g. user manually launched sing-box.exe outside us).
	m.killStrays()
	unblockBinary(m.binPath)
	flushDNS()

	cmd := exec.Command(m.binPath, "run", "-c", m.configPath)
	cmd.Dir = m.workDir
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}

	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		m.log.Logf("vpn.Start: failed to start sing-box: %v", err)
		errStr := err.Error()
		if strings.Contains(errStr, "Application Control") || strings.Contains(errStr, "blocked this file") {
			return fmt.Errorf("Windows Smart App Control заблокировал sing-box.exe. Разблокируйте sing-box.exe в свойствах файла или добавьте папку программы в исключения Защитника Windows")
		}
		return fmt.Errorf("не удалось запустить sing-box: %w", err)
	}
	m.cmd = cmd
	m.log.Logf("vpn.Start: sing-box started, pid=%d", cmd.Process.Pid)

	// Tie the new process to our job. If this fails we still proceed —
	// the taskkill sweeps cover us at the cost of a small race window.
	if m.job != 0 {
		if err := m.assignToJob(cmd.Process.Pid); err != nil {
			m.log.Logf("vpn.Start: warning, could not assign sing-box to job: %v", err)
		} else {
			m.log.Logf("vpn.Start: sing-box assigned to job (will die with us)")
		}
	}

	if stdout != nil {
		go m.log.PipeLines("engine.stdout", stdout)
	}
	if stderr != nil {
		go m.log.PipeLines("engine.stderr", stderr)
	}

	go func(c *exec.Cmd) {
		err := c.Wait()
		m.log.Logf("sing-box exited: err=%v, state=%v", err, c.ProcessState)
	}(cmd)
	return nil
}

func (m *ProcessManager) assignToJob(pid int) error {
	// PROCESS_SET_QUOTA is required by AssignProcessToJobObject;
	// PROCESS_TERMINATE is just future-proofing in case we ever want to
	// kill the process via the handle directly.
	procHandle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(pid),
	)
	if err != nil {
		return fmt.Errorf("OpenProcess(%d): %w", pid, err)
	}
	defer windows.CloseHandle(procHandle)
	if err := windows.AssignProcessToJobObject(m.job, procHandle); err != nil {
		return fmt.Errorf("AssignProcessToJobObject: %w", err)
	}
	return nil
}

// killStrays runs `taskkill /F /IM sing-box.exe`. Safe to call when there's
// nothing to kill — taskkill exits non-zero in that case but we ignore it.
func (m *ProcessManager) killStrays() {
	kill := exec.Command("taskkill", "/F", "/IM", "sing-box.exe")
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	_ = kill.Run()
}

func (m *ProcessManager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd != nil && m.cmd.Process != nil {
		m.log.Logf("vpn.Stop: killing sing-box pid=%d", m.cmd.Process.Pid)
		_ = m.cmd.Process.Kill()
		m.cmd = nil
	}
	m.killStrays()
	flushDNS()
	m.log.Logf("vpn.Stop: done")
	return nil
}

// flushDNS clears the Windows DNS resolver cache to wipe stale FakeIP entries.
func flushDNS() {
	cmd := exec.Command("ipconfig", "/flushdns")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	_ = cmd.Run()
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

// Pid is the running core's process id, or 0 when none is running.
func (m *ProcessManager) Pid() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.isAlive() {
		return 0
	}
	return m.cmd.Process.Pid
}

func (m *ProcessManager) isAlive() bool {
	return m.cmd != nil && m.cmd.Process != nil && m.cmd.ProcessState == nil
}

// FlushDNS clears the Windows resolver cache. Exported for the api layer,
// which resolves the VPN server before the core starts: a cache still
// holding fake-ip answers from the previous session would defeat that.
func FlushDNS() { flushDNS() }
