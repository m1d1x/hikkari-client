//go:build darwin

package api

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"NekkoVPN/core/appversion"
	"NekkoVPN/core/svcclient"
	"NekkoVPN/core/svcproto"
)

// trialWindow: a new version that has been up this long is kept.
const trialWindow = 20 * time.Second

// startUpdates does what the launcher does on Windows: every start of the
// GUI is counted against a version on trial, and one that stays up past
// trialWindow is confirmed. A version that never gets this far is rolled
// back by the service (service.Config.TrialDeadline).
func (c *Core) startUpdates() {
	ctx := c.ctx
	go func() {
		resp, err := svcclient.Call(svcproto.Request{Op: svcproto.OpRecoverUpdate}, 2*time.Minute)
		if err != nil {
			c.logger.Logf("update: recover: %v", err)
			return
		}
		if resp.Recovery != "nothing" {
			c.logger.Logf("update: recover %s (%s -> %s)", resp.Recovery, resp.From, resp.To)
		}
		if resp.Recovery != "on_trial" {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(trialWindow):
		}
		if _, err := svcclient.Call(svcproto.Request{Op: svcproto.OpConfirmUpdate}, time.Minute); err != nil {
			c.logger.Logf("update: confirm: %v", err)
			return
		}
		c.logger.Logf("update: %s confirmed", resp.To)
	}()
}

// RestartToUpdate has the service swap the staged bundle in, then arranges
// for the new bundle to be opened once this process has exited. macOS lets
// the bundle be renamed under a running app, so — unlike Windows — the swap
// happens first, while this GUI is still up to report a failure. The caller
// exits right after this returns.
func (c *Core) RestartToUpdate() error {
	if st := c.UpdateStatus(); st.State != svcproto.UpdateReady {
		return errors.New("нет готового обновления")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// <bundle>/Contents/MacOS/<exe>
	bundle := filepath.Dir(filepath.Dir(filepath.Dir(exe)))
	resp, err := svcclient.Call(svcproto.Request{
		Op:        svcproto.OpApplyUpdate,
		Reconnect: c.IsRunning(),
	}, 10*time.Minute)
	if err != nil {
		return fmt.Errorf("обновление: %w", err)
	}
	c.logger.Logf("update: applied %s -> %s, reopening %s", resp.From, resp.To, bundle)
	// The bundle is the new version by now.
	return reopenAfterExit(bundle)
}

// installedBundle is where the installation lives (macsvc.Bundle). A process
// whose bundle was swapped out finds its own path moved with it, so it opens
// this one, not its own.
const installedBundle = "/Applications/hikkari.app"

// RelaunchApp reopens the installed release once this process has exited: the
// installation was replaced under it (UpdateState.Replaced). The caller exits
// right after this returns.
func (c *Core) RelaunchApp() error {
	c.logger.Logf("app: release %s replaced by the installation, reopening %s", appversion.Own(), installedBundle)
	return reopenAfterExit(installedBundle)
}

// reopenAfterExit has a detached shell, which outlives this process, wait
// for it to exit and open bundle.
func reopenAfterExit(bundle string) error {
	script := `while kill -0 "$1" 2>/dev/null; do sleep 0.2; done; exec /usr/bin/open "$2"`
	cmd := exec.Command("/bin/sh", "-c", script, "sh", strconv.Itoa(os.Getpid()), bundle)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("перезапуск: %w", err)
	}
	return cmd.Process.Release()
}
