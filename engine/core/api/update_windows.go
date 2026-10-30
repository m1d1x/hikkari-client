//go:build windows

package api

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"NekkoVPN/core/svcproto"
)

// startUpdates: on Windows the launcher (Hikkari.exe) recovers and confirms a
// new version around the GUI, so the GUI has nothing to do at start.
func (c *Core) startUpdates() {}

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
