//go:build windows

package api

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"NekkoVPN/core/svcclient"
	"NekkoVPN/core/svcproto"
)

// UpdateStatus is the service's view of updates. A service that cannot be
// reached reads as "unavailable", not as an error: the UI polls this, and a
// missing service already surfaces through StartVPN.
func (c *Core) UpdateStatus() svcproto.UpdateState {
	resp, err := svcclient.Call(svcproto.Request{Op: svcproto.OpStatus}, 5*time.Second)
	if err != nil || resp.Update == nil {
		return svcproto.UpdateState{State: svcproto.UpdateUnavailable}
	}
	return *resp.Update
}

// CheckUpdate asks the service to poll the channel now. The service ignores
// it if it checked within the last few minutes.
func (c *Core) CheckUpdate() error {
	_, err := svcclient.Call(svcproto.Request{Op: svcproto.OpCheckUpdate}, 5*time.Second)
	return err
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
