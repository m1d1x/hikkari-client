//go:build windows

// hikkari.exe — the launcher. The Start menu shortcut points here, at a path
// that never changes between versions, and never at app\nekkobox.exe.
//
//	hikkari.exe                                     launch the GUI
//	hikkari.exe hikkari://…                         a link (the hikkari:// scheme, registered
//	                                                by winsvc): handed to the GUI, which
//	                                                forwards it to its window if one is open
//	hikkari.exe --apply-update --wait-pid N [--reconnect]
//	                                                the GUI's «Перезапустить»: wait for it
//	                                                to exit, swap the update in, launch
//
// Every launch first asks the service to recover (finish an interrupted
// swap, count a start of a version on trial, roll back one that used up its
// starts). A version on trial is then watched for trialWindow: dying in that
// window with an error rolls it back at once and the previous version is
// launched instead; still running — or closed cleanly — confirms it.
//
// Runs unelevated; everything that writes under Program Files is the
// service's job. Built with -H windowsgui: no console window.
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/windows"

	"NekkoVPN/core/logging"
	"NekkoVPN/core/svcclient"
	"NekkoVPN/core/svcproto"
)

// trialWindow: long enough for the GUI to load the engine and draw its
// window — the moments a broken release falls over in — short enough that a
// user who closes the app right away is not mistaken for a crash for long.
const trialWindow = 20 * time.Second

// version is stamped at build time (-ldflags -X main.version=…).
var version = "dev"

var log *logging.Logger

func main() {
	root := exeDir()
	log = logging.New(filepath.Join(userDataDir(), "launcher.log"))
	defer log.Close()
	log.Logf("launcher %s: %v", version, os.Args[1:])

	apply, reconnect, waitPid, link := parseArgs(os.Args[1:])
	gui := filepath.Join(root, "app", "nekkobox.exe")
	if link != "" && guiRunning() {
		// The open window takes the link; nothing about the installation
		// may change under it, so no recover and no trial bookkeeping.
		if _, err := launch(gui, link); err != nil {
			log.Logf("launcher: handing a link to %s: %v", gui, err)
		}
		return
	}
	if apply {
		if waitPid > 0 {
			waitForExit(waitPid, 30*time.Second)
		}
		resp, err := svcclient.Call(svcproto.Request{Op: svcproto.OpApplyUpdate, Reconnect: reconnect}, 10*time.Minute)
		if err != nil {
			// The GUI shows the service's update state; launching the
			// version that is still installed is the right fallback.
			log.Logf("launcher: apply failed: %v", err)
		} else {
			log.Logf("launcher: applied %s -> %s", resp.From, resp.To)
		}
	}

	rec, err := svcclient.Call(svcproto.Request{Op: svcproto.OpRecoverUpdate}, 2*time.Minute)
	if err != nil {
		log.Logf("launcher: recover: %v", err)
	} else if rec.Recovery != "nothing" {
		log.Logf("launcher: recover: %s (%s -> %s)", rec.Recovery, rec.From, rec.To)
	}

	exited, err := launch(gui, link)
	if err != nil {
		log.Logf("launcher: starting %s: %v", gui, err)
		return
	}
	if rec.Recovery == "on_trial" {
		supervise(gui, exited)
	}
}

func parseArgs(args []string) (apply, reconnect bool, waitPid int, link string) {
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "hikkari:") {
			link = args[i]
			continue
		}
		switch args[i] {
		case "--apply-update":
			apply = true
		case "--reconnect":
			reconnect = true
		case "--wait-pid":
			if i+1 < len(args) {
				waitPid, _ = strconv.Atoi(args[i+1])
				i++
			}
		}
	}
	return
}

// guiInstance is the mutex the GUI holds while its window is open
// (app/windows/runner/main.cpp).
const guiInstance = `Local\hikkari-gui`

// guiRunning says whether a GUI window is open in this session.
func guiRunning() bool {
	name, _ := windows.UTF16PtrFromString(guiInstance)
	h, err := windows.OpenMutex(windows.SYNCHRONIZE, false, name)
	if err != nil {
		return false
	}
	windows.CloseHandle(h)
	return true
}

// launch starts the GUI, with link if there is one, and returns a channel
// that yields its exit code.
func launch(gui, link string) (<-chan int, error) {
	var args []string
	if link != "" {
		args = append(args, link)
	}
	cmd := exec.Command(gui, args...)
	cmd.Dir = filepath.Dir(gui)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	exited := make(chan int, 1)
	go func() {
		_ = cmd.Wait()
		exited <- cmd.ProcessState.ExitCode()
	}()
	return exited, nil
}

// supervise watches a version on trial (see the package comment).
func supervise(gui string, exited <-chan int) {
	select {
	case code := <-exited:
		if code != 0 {
			log.Logf("launcher: version on trial exited with %d after <%s, rolling back", code, trialWindow)
			if _, err := svcclient.Call(svcproto.Request{Op: svcproto.OpRollbackUpdate}, 2*time.Minute); err != nil {
				log.Logf("launcher: rollback: %v", err)
				return
			}
			if _, err := launch(gui, ""); err != nil {
				log.Logf("launcher: starting the previous version: %v", err)
			}
			return
		}
	case <-time.After(trialWindow):
	}
	if resp, err := svcclient.Call(svcproto.Request{Op: svcproto.OpConfirmUpdate}, time.Minute); err != nil {
		log.Logf("launcher: confirm: %v", err)
	} else if resp.Update != nil {
		log.Logf("launcher: confirmed %s", resp.Update.Installed)
	}
}

func waitForExit(pid int, limit time.Duration) {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return // already gone
	}
	defer windows.CloseHandle(h)
	if ev, _ := windows.WaitForSingleObject(h, uint32(limit/time.Millisecond)); ev == uint32(windows.WAIT_TIMEOUT) {
		log.Logf("launcher: pid %d still running after %s, applying anyway", pid, limit)
	}
}

func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

// userDataDir is %LOCALAPPDATA%\hikkari, where the GUI keeps its state.
func userDataDir() string {
	local, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
	if err != nil {
		return os.TempDir()
	}
	dir := filepath.Join(local, "hikkari")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}
