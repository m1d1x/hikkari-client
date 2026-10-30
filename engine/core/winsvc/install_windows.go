//go:build windows

package winsvc

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"NekkoVPN/core/svcproto"
)

// Files the service and updater bring with them, beside app\.
var rootFiles = []string{"HikkariService.exe", "hikkari-updater.exe"}

// userDataFiles are the engine's state files. Before the service existed the
// engine kept them next to nekkobox.exe (the portable zip); now they live in
// %LOCALAPPDATA%\Hikkari, where an unelevated GUI can write.
var userDataFiles = []string{
	"profiles.json", "config.json", "custom-services.json", "direct-ip.txt",
	"geoip-cache.json", "ipqs-key.txt", "uptime-history.json",
	"service-uptime-history.json",
}

// IsElevated reports whether this process holds administrator rights.
func IsElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// Install puts this build into %ProgramFiles%\Hikkari and registers and
// starts the service from there. `src` is the unpacked build (the folder
// holding HikkariService.exe and app\); `importFrom` are folders that may
// hold a portable install's data. Re-running it upgrades in place.
func Install(src string, importFrom []string) (string, error) {
	dest, err := InstallDir()
	if err != nil {
		return "", err
	}

	m, err := mgr.Connect()
	if err != nil {
		return "", fmt.Errorf("connecting to the service manager: %w", err)
	}
	defer m.Disconnect()

	// The running service and its core hold files under dest open.
	if s, err := m.OpenService(svcproto.ServiceName); err == nil {
		stopAndWait(s)
		s.Close()
	}

	if !samePath(src, dest) {
		if err := copyBuild(src, dest); err != nil {
			return "", err
		}
	}

	data, err := userDataDir()
	if err != nil {
		return "", err
	}
	migrateUserData(append(importFrom, src, filepath.Join(src, "app")), data)

	exe := filepath.Join(dest, "HikkariService.exe")
	if err := register(m, exe); err != nil {
		return "", err
	}
	return filepath.Join(dest, "app", "nekkobox.exe"), nil
}

// Uninstall stops and removes the service. Files are left in place.
func Uninstall() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connecting to the service manager: %w", err)
	}
	defer m.Disconnect()
	s, err := m.OpenService(svcproto.ServiceName)
	if err != nil {
		return fmt.Errorf("the service is not installed")
	}
	defer s.Close()
	stopAndWait(s)
	return s.Delete()
}

func register(m *mgr.Mgr, exe string) error {
	cfg := mgr.Config{
		DisplayName:  "Hikkari VPN",
		Description:  "Runs the Hikkari VPN core and installs Hikkari updates.",
		StartType:    mgr.StartAutomatic,
		ErrorControl: mgr.ErrorNormal,
	}
	s, err := m.OpenService(svcproto.ServiceName)
	if err == nil {
		cur, err := s.Config()
		if err == nil {
			cur.BinaryPathName = `"` + exe + `"`
			cur.DisplayName, cur.Description, cur.StartType = cfg.DisplayName, cfg.Description, cfg.StartType
			if err := s.UpdateConfig(cur); err != nil {
				s.Close()
				return fmt.Errorf("updating the service: %w", err)
			}
		}
	} else {
		s, err = m.CreateService(svcproto.ServiceName, exe, cfg)
		if err != nil {
			return fmt.Errorf("creating the service: %w", err)
		}
	}
	defer s.Close()
	// Crashed? Back in 5 s, twice; then once a minute. The counter resets daily.
	_ = s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: time.Minute},
	}, 24*60*60)
	if err := s.Start(); err != nil {
		return fmt.Errorf("starting the service: %w", err)
	}
	return nil
}

func stopAndWait(s *mgr.Service) {
	st, err := s.Control(svc.Stop)
	if err != nil {
		return // not running
	}
	for deadline := time.Now().Add(20 * time.Second); st.State != svc.Stopped && time.Now().Before(deadline); {
		time.Sleep(300 * time.Millisecond)
		if st, err = s.Query(); err != nil {
			return
		}
	}
}

// copyBuild replaces dest's binaries and app\ with src's. core\ (service
// state) is kept; leftovers of an update in flight are dropped, since the
// build being installed supersedes them.
func copyBuild(src, dest string) error {
	if _, err := os.Stat(filepath.Join(src, "app", "nekkobox.exe")); err != nil {
		return fmt.Errorf("%s is not a Hikkari build (no app\\nekkobox.exe)", src)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	for _, leftover := range []string{"app.old", ".staging"} {
		_ = os.RemoveAll(filepath.Join(dest, leftover))
	}
	if err := os.RemoveAll(filepath.Join(dest, "app")); err != nil {
		return fmt.Errorf("removing the old app\\ (is Hikkari still open?): %w", err)
	}
	if err := copyTree(filepath.Join(src, "app"), filepath.Join(dest, "app")); err != nil {
		return err
	}
	for _, f := range rootFiles {
		from := filepath.Join(src, f)
		if _, err := os.Stat(from); errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err := copyFile(from, filepath.Join(dest, f)); err != nil {
			return err
		}
	}
	return nil
}

func copyTree(src, dest string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}

// copyFile copies through a fresh file, so the copy carries no Zone.Identifier
// stream (Mark of the Web) from the downloaded zip.
func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := to + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("copying %s: %w", from, err)
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, to)
}

// userDataDir is %LOCALAPPDATA%\Hikkari of the installing user — the same
// folder the GUI computes (app/lib/main.dart).
func userDataDir() (string, error) {
	local, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
	if err != nil {
		return "", fmt.Errorf("locating LocalAppData: %w", err)
	}
	return filepath.Join(local, "Hikkari"), nil
}

// migrateUserData copies a portable install's state files into dest, never
// overwriting what is already there. Best effort: a file that cannot be
// copied is simply not migrated.
func migrateUserData(from []string, dest string) {
	_ = os.MkdirAll(dest, 0o755)
	for _, name := range userDataFiles {
		target := filepath.Join(dest, name)
		if _, err := os.Stat(target); err == nil {
			continue
		}
		for _, dir := range from {
			if strings.TrimSpace(dir) == "" {
				continue
			}
			if err := copyFile(filepath.Join(dir, name), target); err == nil {
				break
			}
			_ = os.Remove(target + ".tmp")
		}
	}
}
