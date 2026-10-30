//go:build windows

package winsvc

// Self-update of the binaries beside app\.
//
// An update replaces app\ only: Windows will not rename a directory holding a
// running executable, so the launcher, the updater and this service live
// outside it. Each release therefore also carries a copy of all three INSIDE
// app\, covered by the signed manifest like every other file, and the
// service puts them in place once the version holding them is settled (no
// version on trial: right after the launcher confirms one, after a rollback,
// at every service start):
//
//	app\X differs from <root>\X (sha256), and is not refused:
//	    app\X → <root>\X.new;  <root>\X → <root>\X.old;  X.new → X
//
// Windows lets a running executable be renamed, so this works for the
// launcher supervising a GUI and for the service itself. The new updater is
// run once (`status`) before it is trusted; a failing one is put back. The
// service cannot test its successor in place, so it hands over to a helper —
// its own, already renamed binary run as `HikkariService.exe.old
// restart-service` — and exits; the helper starts the new service through
// the SCM and waits for it to answer on the pipe. One that does not is
// stopped, the .old put back and started again, and its hash refused
// (core\binaries.json): the same build is not tried twice. The tunnel
// survives the restart through the run marker (Config.ResumeCore).
//
// Installs from before this (1.0.x) run a service that knows none of it. The
// first new code that runs there is the GUI, unelevated; seeing a service
// that does not sync binaries (svcproto.Response.SyncsBinaries) it runs the
// new app\HikkariService.exe adopt once through UAC — see Adopt.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"NekkoVPN/core/logging"
	"NekkoVPN/core/svcclient"
	"NekkoVPN/core/svcproto"
)

const (
	serviceExe  = "HikkariService.exe"
	updaterExe  = "hikkari-updater.exe"
	launcherExe = "Hikkari.exe"

	// healthyWithin: the restarted service must answer on the pipe this
	// soon after the SCM is asked to start it.
	healthyWithin = 90 * time.Second
)

// syncOrder: the service goes last, since replacing it ends this process.
var syncOrder = []string{updaterExe, launcherExe, serviceExe}

// binaries keeps the files beside app\ in line with their copies inside it.
type binaries struct {
	root string
	log  *logging.Logger
}

func (b *binaries) path(name string) string    { return filepath.Join(b.root, name) }
func (b *binaries) appCopy(name string) string { return filepath.Join(b.root, "app", name) }
func (b *binaries) refusedFile() string        { return filepath.Join(b.root, "core", "binaries.json") }

// sync is service.Config.SyncBinaries.
func (b *binaries) sync() (restart bool) {
	b.dropOld()
	refused := b.refused()
	for _, name := range syncOrder {
		want, err := fileHash(b.appCopy(name))
		if err != nil {
			continue // a release without copies (1.0.x), or a rollback to one
		}
		have, _ := fileHash(b.path(name))
		if want == have || refused[name] == want {
			continue
		}
		b.log.Logf("binaries: %s %s -> %s", name, short(have), short(want))
		if err := b.place(name); err != nil {
			b.log.Logf("binaries: placing %s: %v", name, err)
			continue
		}
		switch name {
		case updaterExe:
			if err := b.testUpdater(); err != nil {
				b.log.Logf("binaries: the new updater fails (%v), putting the previous one back", err)
				b.putBack(name, want)
			}
		case serviceExe:
			if err := b.startHelper(); err != nil {
				b.log.Logf("binaries: starting the restart helper: %v, putting the previous service back", err)
				b.putBack(name, "") // not refused: nothing is known to be wrong with it
				continue
			}
			return true
		}
	}
	return false
}

// place swaps app\name in for <root>\name, keeping the current one as .old.
func (b *binaries) place(name string) error {
	dst := b.path(name)
	if err := copyFile(b.appCopy(name), dst+".new"); err != nil {
		return err
	}
	if err := os.Remove(dst + ".old"); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = os.Remove(dst + ".new")
		return fmt.Errorf("an older %s.old is still in use: %w", name, err)
	}
	if err := os.Rename(dst, dst+".old"); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = os.Remove(dst + ".new")
		return err
	}
	if err := os.Rename(dst+".new", dst); err != nil {
		_ = os.Rename(dst+".old", dst)
		return err
	}
	return nil
}

// putBack restores <root>\name.old over a new copy that failed, and refuses
// the failed build's hash if one is given.
func (b *binaries) putBack(name, refuse string) {
	dst := b.path(name)
	if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
		// Still mapped (a process that has not quite exited): move it aside.
		_ = os.Rename(dst, dst+".bad")
	}
	if err := os.Rename(dst+".old", dst); err != nil {
		b.log.Logf("binaries: putting %s back: %v", name, err)
	}
	if refuse != "" {
		b.refuse(name, refuse)
	}
}

// dropOld removes what earlier swaps left. A .old still running (the
// launcher, the restart helper) cannot be deleted and stays until next time.
func (b *binaries) dropOld() {
	for _, name := range syncOrder {
		for _, suffix := range []string{".old", ".bad", ".new"} {
			_ = os.Remove(b.path(name) + suffix)
		}
	}
}

func (b *binaries) testUpdater() error {
	cmd := exec.Command(b.path(updaterExe), "status", "--root", b.root, "--layout", "windows")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	out, err := cmd.Output()
	if err != nil {
		return err
	}
	if !strings.Contains(string(out), `"event":"status"`) {
		return fmt.Errorf("unexpected output %q", out)
	}
	return nil
}

// startHelper runs the previous service binary — this very program, renamed
// to .old by place — as `restart-service`, detached, so it outlives us.
func (b *binaries) startHelper() error {
	cmd := exec.Command(b.path(serviceExe)+".old", "restart-service")
	cmd.Dir = b.root
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow | windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP,
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func (b *binaries) refused() map[string]string {
	m := map[string]string{}
	if data, err := os.ReadFile(b.refusedFile()); err == nil {
		_ = json.Unmarshal(data, &m)
	}
	return m
}

func (b *binaries) refuse(name, hash string) {
	m := b.refused()
	m[name] = hash
	data, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(b.refusedFile(), data, 0o644); err != nil {
		b.log.Logf("binaries: recording the refusal: %v", err)
	}
}

// RestartService is `HikkariService.exe.old restart-service`: started by the
// service that has just put a new HikkariService.exe in place and is exiting.
// It starts the new one and rolls it back if it does not come up.
func RestartService() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	root := filepath.Dir(exe)
	if want, err := InstallDir(); err != nil || !samePath(root, want) {
		return fmt.Errorf("restart-service runs only from the installation, not %s", root)
	}
	b := &binaries{root: root, log: logging.New(filepath.Join(root, "core", "service.log"))}
	defer b.log.Close()

	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(svcproto.ServiceName)
	if err != nil {
		return err
	}
	defer s.Close()

	if !waitState(s, svc.Stopped, time.Minute) {
		b.log.Logf("binaries: the previous service did not stop, stopping it")
		stopAndWait(s)
	}
	err = startHealthy(s)
	if err == nil {
		b.log.Logf("binaries: the new service is up")
		return nil
	}
	b.log.Logf("binaries: the new service did not come up (%v), putting the previous one back", err)
	stopAndWait(s)
	bad, _ := fileHash(b.path(serviceExe))
	b.putBack(serviceExe, bad)
	if err := startHealthy(s); err != nil {
		b.log.Logf("binaries: the previous service did not come back either: %v", err)
		return err
	}
	b.log.Logf("binaries: the previous service is back")
	return nil
}

// startHealthy starts the service (tolerating one the SCM's recovery action
// already started) and waits until it answers on the pipe.
func startHealthy(s *mgr.Service) error {
	if err := s.Start(); err != nil && !errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
		return fmt.Errorf("start: %w", err)
	}
	deadline := time.Now().Add(healthyWithin)
	for time.Now().Before(deadline) {
		if st, err := s.Query(); err == nil && st.State == svc.Stopped {
			return errors.New("it stopped")
		}
		if resp, err := svcclient.Call(svcproto.Request{Op: svcproto.OpStatus}, 3*time.Second); err == nil && resp.OK {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("no answer on the pipe within %s", healthyWithin)
}

func waitState(s *mgr.Service, want svc.State, limit time.Duration) bool {
	for deadline := time.Now().Add(limit); time.Now().Before(deadline); time.Sleep(300 * time.Millisecond) {
		st, err := s.Query()
		if err != nil {
			return false
		}
		if st.State == want {
			return true
		}
	}
	return false
}

// Adopt is `app\HikkariService.exe adopt`, elevated: an install whose
// service predates self-update (1.0.x) gets this release's binaries beside
// app\ once, and from then on keeps them up to date by itself. The service is
// stopped around the swap — it is the old one and has no say in it — and the
// tunnel comes back through the run marker. Nothing happens while a version
// is on trial: the GUI asks again at its next start.
func Adopt() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	appDir := filepath.Dir(exe)
	root := filepath.Dir(appDir)
	if want, err := InstallDir(); err != nil || !samePath(root, want) || !strings.EqualFold(filepath.Base(appDir), "app") {
		return fmt.Errorf("adopt runs only from %s\\app, not %s", want, appDir)
	}
	b := &binaries{root: root, log: logging.New(filepath.Join(root, "core", "service.log"))}
	defer b.log.Close()

	cmd := exec.Command(b.path(updaterExe), "status", "--root", root, "--layout", "windows")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("updater status: %w", err)
	}
	var st struct {
		Pending json.RawMessage `json:"pending"`
	}
	_ = json.Unmarshal(out, &st)
	if len(st.Pending) > 0 && string(st.Pending) != "null" {
		return errors.New("a version is on trial; adopting after it is confirmed")
	}

	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(svcproto.ServiceName)
	if err != nil {
		return err
	}
	defer s.Close()

	b.log.Logf("binaries: adopting the binaries of %s (elevated, from the GUI)", installedVersion(root))
	stopAndWait(s)
	for _, name := range syncOrder {
		want, err := fileHash(b.appCopy(name))
		if err != nil {
			continue
		}
		if have, _ := fileHash(b.path(name)); have == want {
			continue
		}
		if err := b.place(name); err != nil {
			b.log.Logf("binaries: placing %s: %v", name, err)
		}
	}
	if err := startHealthy(s); err != nil {
		b.log.Logf("binaries: the adopted service did not come up (%v), putting the previous one back", err)
		stopAndWait(s)
		b.putBack(serviceExe, "")
		return startHealthy(s)
	}
	return nil
}

func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func short(hash string) string {
	if len(hash) < 12 {
		return "none"
	}
	return hash[:12]
}
