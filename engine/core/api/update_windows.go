//go:build windows

package api

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"golang.org/x/sys/windows"

	"NekkoVPN/core/svcclient"
	"NekkoVPN/core/svcproto"
)

// startUpdates: on Windows the launcher (Hikkari.exe) recovers and confirms a
// new version around the GUI. What is left for the GUI is an install whose
// service predates self-update (1.0.x): it never puts the launcher, the
// updater or itself in line with their copies in app\, so the GUI — the
// first new code to run there — has the new service binary adopt them.
func (c *Core) startUpdates() { go c.adoptBinaries() }

// adoptAfter: past the launcher's confirmation of a version on trial (20 s);
// adopt refuses to run while one is on trial.
const adoptAfter = 30 * time.Second

// adoptBinaries runs app\HikkariService.exe adopt through UAC, once per
// launch, when the service does not sync binaries itself and its own binary
// is not the one this release brings. Declined, it asks again next launch.
func (c *Core) adoptBinaries() {
	time.Sleep(adoptAfter)
	resp, err := svcclient.Call(svcproto.Request{Op: svcproto.OpStatus}, 5*time.Second)
	if err != nil || !resp.OK || resp.SyncsBinaries {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	appDir := filepath.Dir(exe) // <root>\app\nekkobox.exe
	adopter := filepath.Join(appDir, "HikkariService.exe")
	want, err := fileHash(adopter)
	if err != nil {
		return // a build without the copies
	}
	if have, _ := fileHash(filepath.Join(filepath.Dir(appDir), "HikkariService.exe")); have == want {
		return
	}
	c.logger.Logf("update: service %s does not update itself, asking to adopt this release's binaries", resp.Version)
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(adopter)
	params, _ := windows.UTF16PtrFromString("adopt")
	dir, _ := windows.UTF16PtrFromString(appDir)
	if err := windows.ShellExecute(0, verb, file, params, dir, windows.SW_HIDE); err != nil {
		c.logger.Logf("update: adopt: %v", err)
	}
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

// RestartToUpdate hands over to the launcher (<root>\Hikkari.exe), which
// waits for this process to exit, has the service swap the staged version in
// and starts the new GUI. The caller exits right after this returns.
func (c *Core) RestartToUpdate() error {
	if st := c.UpdateStatus(); st.State != svcproto.UpdateReady {
		return errors.New("нет готового обновления")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	root := filepath.Dir(filepath.Dir(exe)) // <root>\app\nekkobox.exe
	launcher := filepath.Join(root, "Hikkari.exe")
	if _, err := os.Stat(launcher); err != nil {
		return fmt.Errorf("лаунчер не найден: %s", launcher)
	}
	args := []string{"--apply-update", "--wait-pid", strconv.Itoa(os.Getpid())}
	if c.IsRunning() {
		args = append(args, "--reconnect")
	}
	cmd := exec.Command(launcher, args...)
	cmd.Dir = root
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("запуск лаунчера: %w", err)
	}
	c.logger.Logf("update: handed over to %s %v", launcher, args)
	return cmd.Process.Release()
}
