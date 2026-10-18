//go:build darwin

package vpn

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ProcessManager runs sing-box on macOS under a root LaunchDaemon.
//
// The core needs root (TUN device + routes) and macOS gives an app no way to
// elevate itself. Spawning it from an authenticated shell (osascript /
// AuthorizationExecuteWithPrivileges) does start it, but nothing owns the
// result: such a process gets reaped roughly 30 minutes in, and every
// restart costs another password prompt. So the privileged half lives in
// launchd, which never dies, restarts on crash and survives reboots.
//
// That privileged half is a small supervisor script, not sing-box itself.
// launchd's own KeepAlive/PathState condition looked like the natural fit —
// it does start a job when the watched file appears — but it will not stop a
// running one when the file goes away, so "disconnect" had no effect. The
// supervisor closes that loop: it polls the sentinel once a second and
// starts or stops the core to match.
//
//	<baseDir>/singbox.run exists  → core should be running
//	                     removed  → core gets terminated
//
// Both are plain user-level file operations, so connect and disconnect never
// ask for anything. The password is asked once, when the daemon is installed
// (or reinstalled, when any generated file changes — e.g. the app moved).
// The supervisor also brings back a core that died on its own, which is the
// crash recovery the Windows side gets from its Job Object.
//
// Consequence worth knowing: the tunnel is owned by launchd, not by us, so it
// survives an app crash instead of dropping traffic to the clear. A clean app
// shutdown removes the sentinel and takes the core down with it.
type ProcessManager struct {
	binPath    string
	configPath string
	workDir    string
	log        LineLogger

	mu sync.Mutex
}

const (
	daemonLabel  = "com.nekkobox.singbox"
	daemonPlist  = "/Library/LaunchDaemons/" + daemonLabel + ".plist"
	daemonDir    = "/Library/Application Support/nekkobox"
	supervisorSh = daemonDir + "/singbox-supervisor.sh"
	// The core the daemon runs is a root-owned copy, not the one inside the
	// app bundle. A bundle living in a build directory gets rebuilt, moved
	// or cleaned away under a running tunnel — and a root daemon executing a
	// user-writable binary would hand root to anything able to overwrite it.
	installedBin = daemonDir + "/sing-box"
)

func NewProcessManager(binPath, configPath, workDir string, log LineLogger) *ProcessManager {
	return &ProcessManager{
		binPath:    binPath,
		configPath: configPath,
		workDir:    workDir,
		log:        log,
	}
}

// runFile is the sentinel the supervisor watches: present = core should run.
func (m *ProcessManager) runFile() string { return filepath.Join(m.workDir, "singbox.run") }
func (m *ProcessManager) logFile() string { return filepath.Join(m.workDir, "singbox.log") }

func (m *ProcessManager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	bin, err := m.resolveBinary()
	if err != nil {
		m.log.Logf("vpn.Start: %v", err)
		return err
	}
	if _, err := os.Stat(m.configPath); err != nil {
		return fmt.Errorf("конфиг sing-box не найден: %s", m.configPath)
	}

	plist, supervisor := m.plist(), m.supervisor(installedBin)
	installed := fileHas(daemonPlist, plist) &&
		fileHas(supervisorSh, supervisor) &&
		sameFile(bin, installedBin)

	// A running core is only "nothing to do" when it is ours. If the daemon
	// on disk is a different one — an older layout, a previous install
	// location — the core it started answers the Clash API but ignores our
	// sentinel, so Stop would have nothing to pull. Reinstalling boots that
	// job out and takes its core with it.
	if ClashAPIRunning() && installed {
		m.log.Logf("vpn.Start: core already answering on the Clash API, no-op")
		return m.raise()
	}
	if !installed {
		m.log.Logf("vpn.Start: installing launchd daemon %s", daemonLabel)
		if err := m.installDaemon(plist, supervisor, bin); err != nil {
			return err
		}
	}

	if err := m.raise(); err != nil {
		return err
	}
	if m.waitForCore(20 * time.Second) {
		m.log.Logf("vpn.Start: sing-box up, log=%s", m.logFile())
		return nil
	}

	// Files matched but nothing came up — the daemon is there and stale
	// (never bootstrapped after a reboot, say). Worth one reinstall before
	// giving up; this is the only path where a routine start can still ask
	// for a password.
	if installed {
		m.log.Logf("vpn.Start: daemon did not react, reinstalling")
		if err := m.installDaemon(plist, supervisor, bin); err != nil {
			return err
		}
		if m.waitForCore(20 * time.Second) {
			m.log.Logf("vpn.Start: sing-box up after reinstall, log=%s", m.logFile())
			return nil
		}
	}

	_ = os.Remove(m.runFile())
	return fmt.Errorf("sing-box не поднялся, смотри %s", m.logFile())
}

func (m *ProcessManager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := os.Remove(m.runFile()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("не удалось убрать %s: %w", m.runFile(), err)
	}
	m.log.Logf("vpn.Stop: sentinel removed, waiting for core to go down")
	if m.waitForCoreGone(15 * time.Second) {
		m.log.Logf("vpn.Stop: done")
		return nil
	}

	// The core outlived its sentinel, so whatever is supervising it is not
	// the daemon we generate — an older layout, or a plist edited out from
	// under us. Reinstalling boots that job out (killing its core with it)
	// and leaves ours loaded but idle, since the sentinel is already gone.
	// Costs one password prompt, and only in this recovery path.
	if bin, err := m.resolveBinary(); err == nil {
		plist, supervisor := m.plist(), m.supervisor(installedBin)
		if !fileHas(daemonPlist, plist) || !fileHas(supervisorSh, supervisor) {
			m.log.Logf("vpn.Stop: core is owned by a stale daemon, reinstalling to evict it")
			if err := m.installDaemon(plist, supervisor, bin); err != nil {
				return err
			}
			if m.waitForCoreGone(15 * time.Second) {
				m.log.Logf("vpn.Stop: done")
				return nil
			}
		}
	}

	m.log.Logf("vpn.Stop: core still answering")
	return fmt.Errorf("sing-box не остановился, смотри %s", m.logFile())
}

func (m *ProcessManager) Restart() error {
	m.log.Logf("vpn.Restart: begin")
	if err := m.Stop(); err != nil {
		m.log.Logf("vpn.Restart: stop error: %v", err)
	}
	time.Sleep(1500 * time.Millisecond)
	err := m.Start()
	m.log.Logf("vpn.Restart: end, err=%v", err)
	return err
}

func (m *ProcessManager) raise() error {
	if err := os.WriteFile(m.runFile(), []byte("up\n"), 0644); err != nil {
		return fmt.Errorf("не удалось создать %s: %w", m.runFile(), err)
	}
	return nil
}

func (m *ProcessManager) waitForCore(d time.Duration) bool {
	return waitUntil(d, ClashAPIRunning)
}

func (m *ProcessManager) waitForCoreGone(d time.Duration) bool {
	return waitUntil(d, func() bool { return !ClashAPIRunning() })
}

func waitUntil(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(250 * time.Millisecond)
	}
	return false
}

// fileHas reports whether path already holds exactly this content. Both
// generated files are compared byte-for-byte to decide whether elevation is
// needed — that comparison is what makes every start after the first one
// password-free.
func fileHas(path, want string) bool {
	got, err := os.ReadFile(path)
	return err == nil && string(got) == want
}

// sameFile reports whether dst is already a copy of src. Used on the core
// binary, where rereading 80-odd MB beats asking for a password: size rules
// out the common case, and the hash covers a same-size rebuild. Mtime is
// deliberately not part of it — every app build stamps a fresh one on the
// bundled copy without changing a byte.
func sameFile(src, dst string) bool {
	si, err := os.Stat(src)
	if err != nil {
		return false
	}
	di, err := os.Stat(dst)
	if err != nil || si.Size() != di.Size() {
		return false
	}
	sh, err := fileSum(src)
	if err != nil {
		return false
	}
	dh, err := fileSum(dst)
	return err == nil && sh == dh
}

func fileSum(path string) ([sha256.Size]byte, error) {
	var sum [sha256.Size]byte
	f, err := os.Open(path)
	if err != nil {
		return sum, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return sum, err
	}
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

// installDaemon puts the supervisor and the plist into root-owned locations
// and bootstraps the job, in a single elevated shell — one password prompt.
// Both files land as root:wheel and stay non-writable by the user: launchd
// runs the supervisor as root, so a user-writable copy would hand root to
// anything able to write that file.
func (m *ProcessManager) installDaemon(plist, supervisor, srcBin string) error {
	tmpPlist := filepath.Join(m.workDir, daemonLabel+".plist")
	tmpSup := filepath.Join(m.workDir, "singbox-supervisor.sh")
	if err := os.WriteFile(tmpPlist, []byte(plist), 0644); err != nil {
		return fmt.Errorf("не удалось записать %s: %w", tmpPlist, err)
	}
	if err := os.WriteFile(tmpSup, []byte(supervisor), 0644); err != nil {
		return fmt.Errorf("не удалось записать %s: %w", tmpSup, err)
	}
	defer os.Remove(tmpSup)
	defer os.Remove(tmpPlist)

	script := filepath.Join(m.workDir, "install-daemon.sh")
	if err := os.WriteFile(script, []byte(installScript(srcBin, tmpSup, tmpPlist)), 0700); err != nil {
		return fmt.Errorf("не удалось записать %s: %w", script, err)
	}
	defer os.Remove(script)

	// argv-passing keeps the script path out of the AppleScript source, so
	// paths with spaces (the default base dir has one) need no escaping.
	cmd := exec.Command("osascript",
		"-e", "on run argv",
		"-e", `do shell script "/bin/sh " & quoted form of (item 1 of argv) `+
			`with prompt "Nekkobox устанавливает служебный процесс VPN-ядра." `+
			`with administrator privileges`,
		"-e", "end run",
		script,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "-128") || strings.Contains(msg, "User canceled") {
			m.log.Logf("vpn: daemon install cancelled by user")
			return fmt.Errorf("установка отменена: без прав администратора ядро не поднять")
		}
		m.log.Logf("vpn: daemon install failed: %v (%s)", err, msg)
		return fmt.Errorf("не удалось установить служебный процесс: %s", msg)
	}
	m.log.Logf("vpn: launchd daemon %s installed", daemonLabel)
	return nil
}

// installScript renders the root-side install: put the three files in place,
// then hand the job over to launchd. Everything here runs as root exactly
// once per install, so it is worth being explicit about launchd's quirks
// rather than hoping a retry at the Go level covers them.
func installScript(srcBin, tmpSup, tmpPlist string) string {
	return fmt.Sprintf(`#!/bin/sh
# Generated by nekkobox — installs the sing-box supervisor LaunchDaemon.
set -e
install -d -o root -g wheel -m 755 %s
install -o root -g wheel -m 755 %s %s
install -o root -g wheel -m 755 %s %s
install -o root -g wheel -m 644 %s %s

LABEL=%s
PLIST=%s

# bootout returns before the job is actually gone, and bootstrapping into a
# label that is still unloading fails with "Input/output error (5)". Wait it
# out instead, then retry a few times for whatever is still settling.
launchctl bootout "system/$LABEL" 2>/dev/null || true
n=0
while launchctl print "system/$LABEL" >/dev/null 2>&1 && [ $n -lt 50 ]; do
  sleep 0.2
  n=$((n + 1))
done

# A label disabled by hand (or by an earlier bootout -w) stays disabled
# across installs and would make every bootstrap fail.
launchctl enable "system/$LABEL" 2>/dev/null || true

n=0
until launchctl bootstrap system "$PLIST"; do
  n=$((n + 1))
  if [ $n -ge 5 ]; then
    echo "launchctl bootstrap failed after $n attempts" >&2
    exit 1
  fi
  sleep 1
done
`,
		shellQuote(daemonDir),
		shellQuote(srcBin), shellQuote(installedBin),
		shellQuote(tmpSup), shellQuote(supervisorSh),
		shellQuote(tmpPlist), shellQuote(daemonPlist),
		shellQuote(daemonLabel),
		shellQuote(daemonPlist),
	)
}

// supervisor renders the root-side script launchd keeps running. Keep it
// deterministic — it is compared byte-for-byte against the installed copy.
func (m *ProcessManager) supervisor(bin string) string {
	return fmt.Sprintf(`#!/bin/sh
# Generated by nekkobox — do not edit. Runs as root under launchd (%s)
# and keeps the sing-box core in sync with the sentinel file: present means
# the user asked for the tunnel, absent means they did not.
BIN=%s
CFG=%s
WORKDIR=%s
RUNFILE=%s
SB=""

stop_core() {
  [ -n "$SB" ] || return 0
  if kill -0 "$SB" 2>/dev/null; then
    kill "$SB" 2>/dev/null
    n=0
    while kill -0 "$SB" 2>/dev/null && [ $n -lt 20 ]; do
      sleep 0.5
      n=$((n + 1))
    done
    kill -9 "$SB" 2>/dev/null
  fi
  wait "$SB" 2>/dev/null
  SB=""
  echo "supervisor: core stopped"
}

trap 'stop_core; exit 0' TERM INT

while :; do
  if [ -f "$RUNFILE" ]; then
    # Covers both "user just connected" and "core died on its own".
    if [ -z "$SB" ] || ! kill -0 "$SB" 2>/dev/null; then
      "$BIN" run -c "$CFG" -D "$WORKDIR" &
      SB=$!
      echo "supervisor: started sing-box pid=$SB"
    fi
  else
    stop_core
  fi
  sleep 1
done
`,
		daemonLabel,
		shellQuote(bin),
		shellQuote(m.configPath),
		shellQuote(m.workDir),
		shellQuote(m.runFile()),
	)
}

// plist renders the daemon definition: run the supervisor, always, and bring
// it back if it ever exits. Deterministic for the same reason as above.
func (m *ProcessManager) plist() string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>/bin/sh</string>
		<string>%s</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
	<key>ProcessType</key>
	<string>Interactive</string>
	<key>ThrottleInterval</key>
	<integer>5</integer>
</dict>
</plist>
`, daemonLabel, xmlEscape(supervisorSh), xmlEscape(m.logFile()), xmlEscape(m.logFile()))
}

// resolveBinary finds the sing-box core to install from — the daemon itself
// runs the root-owned copy at installedBin. macOS builds don't commit the
// binary, so we look in the order that matches how it can get there:
// alongside the engine's state dir, inside the .app bundle's Resources
// (where a packaged build ships it), then whatever is on $PATH or in the
// usual Homebrew prefixes.
func (m *ProcessManager) resolveBinary() (string, error) {
	candidates := []string{m.binPath}
	if exe, err := os.Executable(); err == nil {
		// …/Nekkobox.app/Contents/MacOS/nekkobox → …/Contents/Resources
		macOSDir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(filepath.Dir(macOSDir), "Resources", "sing-box"),
			filepath.Join(macOSDir, "sing-box"),
		)
	}
	candidates = append(candidates,
		"/opt/homebrew/bin/sing-box",
		"/usr/local/bin/sing-box",
	)
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c, nil
		}
	}
	if p, err := exec.LookPath("sing-box"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("бинарь sing-box не найден (искал %s, Resources бандла, $PATH)", m.binPath)
}

// shellQuote wraps a string in single quotes for /bin/sh, escaping any
// single quote inside it.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}
