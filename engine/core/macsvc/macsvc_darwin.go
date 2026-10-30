//go:build darwin

// Package macsvc is HikkariService on macOS: a root LaunchDaemon around the
// platform-neutral service in core/service, and its installation.
//
//	/Applications/Hikkari.app                  root:wheel, installed by the .pkg,
//	    Contents/MacOS/nekkobox                replaced whole by an update
//	    Contents/MacOS/HikkariService          this daemon (net.hikkari.service)
//	    Contents/MacOS/hikkari-updater
//	    Contents/Resources/sing-box, VERSION
//	/Library/LaunchDaemons/net.hikkari.service.plist
//	/var/db/hikkari/                           the daemon's own: config.json, cache.db,
//	                                           service.log, run
//	/var/run/hikkari-service.sock              the GUI's way in (svcproto)
//
// The daemon executes binaries from the bundle as root, so it refuses to run
// unless the bundle is root-owned and writable by root alone — what the .pkg
// gives it, and what a bundle dragged in by hand or built in place does not
// have. (Like every app in /Applications, the bundle can still be renamed by
// an administrator account: macOS treats admin as able to become root.)
package macsvc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"NekkoVPN/core/service"
	"NekkoVPN/core/svcproto"
)

const (
	Label     = "net.hikkari.service"
	PlistPath = "/Library/LaunchDaemons/" + Label + ".plist"
	// Bundle is where the .pkg installs the app; the updater's layout
	// (/Applications + Hikkari.app) says the same.
	Bundle  = "/Applications/Hikkari.app"
	CoreDir = "/var/db/hikkari"

	serviceBin = Bundle + "/Contents/MacOS/HikkariService"
	updaterBin = Bundle + "/Contents/MacOS/hikkari-updater"
	coreBin    = Bundle + "/Contents/Resources/sing-box"

	// A new version swapped in and not confirmed this long after is rolled
	// back by the daemon: the GUI confirms ~20 s after it starts, and the
	// swap opens it right away.
	trialDeadline = 90 * time.Second
)

// Legacy: before the service, the GUI installed its own LaunchDaemon that
// supervised a root copy of sing-box through a sentinel file.
const (
	legacyLabel = "com.nekkobox.singbox"
	legacyPlist = "/Library/LaunchDaemons/" + legacyLabel + ".plist"
	legacyDir   = "/Library/Application Support/nekkobox"
)

// Run serves until launchd stops the daemon (SIGTERM) or an update replaced
// it. Either way it returns, and launchd (KeepAlive) starts the binary that
// is now at serviceBin.
func Run() error {
	if os.Geteuid() != 0 {
		return errors.New("HikkariService must run as root (it is a LaunchDaemon)")
	}
	if err := checkBundle(); err != nil {
		return err
	}
	srv, err := service.NewServer(service.Config{
		CoreDir: CoreDir,
		CoreBin: coreBin,
		// sing-box resolves relative paths against its working directory;
		// the sanitiser allows none, and the daemon's own dir is the safe one.
		CoreWorkDir: CoreDir,
		Updater:     updaterBin,
		UpdaterArgs: []string{"--root", filepath.Dir(Bundle), "--layout", "macos"},
		Listen:      listen,
		Endpoint:    svcproto.SocketPath,
		// The swap replaces this binary; launchd starts the new one.
		RestartAfterApply: true,
		TrialDeadline:     trialDeadline,
	})
	if err != nil {
		return err
	}
	ctx, stop := signalContext()
	defer stop()
	err = srv.Serve(ctx)
	if errors.Is(err, service.ErrRestart) {
		return nil
	}
	return err
}

// listen opens the control socket: any local account may connect — the
// same "interactive users" the Windows pipe admits — and the requests are
// what svcproto.Sanitize and the update operations allow, nothing more.
func listen() (net.Listener, error) {
	_ = os.Remove(svcproto.SocketPath)
	l, err := net.Listen("unix", svcproto.SocketPath)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(svcproto.SocketPath, 0o666); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}

func signalContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan os.Signal, 1)
	signalNotify(ch)
	go func() {
		select {
		case <-ch:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

// checkBundle: everything the daemon executes as root must be modifiable by
// root alone.
func checkBundle() error {
	for _, p := range []string{
		Bundle, Bundle + "/Contents", Bundle + "/Contents/MacOS", Bundle + "/Contents/Resources",
		serviceBin, updaterBin, coreBin,
	} {
		if err := rootOnly(p); err != nil {
			return fmt.Errorf("%w — reinstall Hikkari from the .pkg", err)
		}
	}
	return nil
}

func rootOnly(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%s: no ownership information", path)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink", path)
	}
	if st.Uid != 0 || fi.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s is not root-owned and root-only writable (uid %d, mode %v)", path, st.Uid, fi.Mode().Perm())
	}
	return nil
}

// Install registers the daemon for the bundle the .pkg just put in place
// and starts it, replacing the legacy daemon. Run by the package's
// postinstall script, as root.
func Install() error {
	if os.Geteuid() != 0 {
		return errors.New("install must run as root")
	}
	if err := checkBundle(); err != nil {
		return err
	}
	removeLegacy()
	_ = launchctl("bootout", "system/"+Label)
	if err := os.WriteFile(PlistPath, []byte(plist()), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", PlistPath, err)
	}
	if err := os.MkdirAll(CoreDir, 0o755); err != nil {
		return err
	}
	if err := launchctl("bootstrap", "system", PlistPath); err != nil {
		return fmt.Errorf("starting the daemon: %w", err)
	}
	// Ready when the socket answers.
	for i := 0; i < 50; i++ {
		if conn, err := net.Dial("unix", svcproto.SocketPath); err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("the daemon did not open %s", svcproto.SocketPath)
}

// Uninstall stops the daemon (taking the core down) and removes everything
// the package installed, the bundle included. GUI data in the user's
// Library stays.
func Uninstall() error {
	if os.Geteuid() != 0 {
		return errors.New("uninstall must run as root (sudo)")
	}
	_ = launchctl("bootout", "system/"+Label)
	removeLegacy()
	stopProcessesUnder(Bundle + "/Contents/MacOS/")
	var errs []string
	for _, p := range []string{
		PlistPath, svcproto.SocketPath, CoreDir,
		Bundle, Bundle + ".old", filepath.Join(filepath.Dir(Bundle), ".Hikkari.staging"),
	} {
		if err := os.RemoveAll(p); err != nil {
			errs = append(errs, err.Error())
		}
	}
	_ = exec.Command("/usr/sbin/pkgutil", "--forget", "net.hikkari.pkg").Run()
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// stopProcessesUnder ends every process running a binary under dir — the
// GUI, mostly — except this one, which may be running from there too.
func stopProcessesUnder(dir string) {
	out, _ := exec.Command("/usr/bin/pgrep", "-f", dir).Output()
	for _, field := range strings.Fields(string(out)) {
		pid, err := strconv.Atoi(field)
		if err != nil || pid == os.Getpid() {
			continue
		}
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
}

func removeLegacy() {
	if _, err := os.Stat(legacyPlist); err != nil {
		return
	}
	_ = launchctl("bootout", "system/"+legacyLabel)
	_ = os.Remove(legacyPlist)
	_ = os.RemoveAll(legacyDir)
}

func launchctl(args ...string) error {
	out, err := exec.Command("/bin/launchctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// plist: KeepAlive restarts the daemon whenever it exits — after a crash,
// and after an update swapped its binary (it exits on purpose then), which
// is also why ThrottleInterval is short. AbandonProcessGroup stays false so
// launchd takes sing-box down with the daemon.
func plist() string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + Label + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + serviceBin + `</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ThrottleInterval</key>
	<integer>2</integer>
	<key>StandardErrorPath</key>
	<string>` + CoreDir + `/daemon.err</string>
</dict>
</plist>
`
}
