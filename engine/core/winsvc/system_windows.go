//go:build windows

package winsvc

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// The OS-facing parts of an installation: shortcuts, the "Apps & features"
// entry, and the processes that hold its files open.

const (
	uninstallKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\Hikkari`
	productName  = "Hikkari VPN"
	shortcutName = "Hikkari.lnk"
)

// shortcutPaths are the Start menu and desktop shortcuts, for all users: the
// service is machine-wide, so is the app.
func shortcutPaths() ([]string, error) {
	var out []string
	for _, id := range []*windows.KNOWNFOLDERID{windows.FOLDERID_CommonPrograms, windows.FOLDERID_PublicDesktop} {
		dir, err := windows.KnownFolderPath(id, 0)
		if err != nil {
			return nil, fmt.Errorf("locating the shortcut folder: %w", err)
		}
		out = append(out, filepath.Join(dir, shortcutName))
	}
	return out, nil
}

// createShortcuts points the shortcuts at the launcher — a path no update
// ever changes. Through WScript.Shell: the one .lnk writer every Windows has,
// without hand-rolling IShellLink over COM.
func createShortcuts(launcher string) error {
	paths, err := shortcutPaths()
	if err != nil {
		return err
	}
	for _, lnk := range paths {
		script := fmt.Sprintf(
			`$s = (New-Object -ComObject WScript.Shell).CreateShortcut(%s); `+
				`$s.TargetPath = %s; $s.WorkingDirectory = %s; $s.IconLocation = %s; `+
				`$s.Description = %s; $s.Save()`,
			psQuote(lnk), psQuote(launcher), psQuote(filepath.Dir(launcher)),
			psQuote(launcher+",0"), psQuote(productName))
		if out, err := powershell(script); err != nil {
			return fmt.Errorf("creating %s: %v: %s", lnk, err, out)
		}
	}
	return nil
}

func removeShortcuts() {
	paths, err := shortcutPaths()
	if err != nil {
		return
	}
	for _, lnk := range paths {
		_ = os.Remove(lnk)
	}
}

// writeUninstallEntry registers the app in "Apps & features".
func writeUninstallEntry(dest, version string) error {
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, uninstallKey, registry.ALL_ACCESS)
	if err != nil {
		return fmt.Errorf("creating the uninstall entry: %w", err)
	}
	defer k.Close()
	service := filepath.Join(dest, "HikkariService.exe")
	str := map[string]string{
		"DisplayName":     productName,
		"DisplayVersion":  version,
		"Publisher":       "Hikkari",
		"InstallLocation": dest,
		"DisplayIcon":     filepath.Join(dest, "Hikkari.exe"),
		"UninstallString": `"` + service + `" uninstall`,
		// Scripted removal (winget and friends) asks for this one.
		"QuietUninstallString": `"` + service + `" uninstall --quiet`,
	}
	for name, v := range str {
		if err := k.SetStringValue(name, v); err != nil {
			return err
		}
	}
	for name, v := range map[string]uint32{
		"NoModify":      1,
		"NoRepair":      1,
		"EstimatedSize": uint32(dirSize(dest) / 1024),
	} {
		if err := k.SetDWordValue(name, v); err != nil {
			return err
		}
	}
	return nil
}

// setDisplayVersion keeps "Apps & features" truthful after an update.
func setDisplayVersion(version string) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, uninstallKey, registry.SET_VALUE)
	if err != nil {
		return // not installed through the installer (a dev `debug` run)
	}
	defer k.Close()
	_ = k.SetStringValue("DisplayVersion", version)
}

func removeUninstallEntry() {
	_ = registry.DeleteKey(registry.LOCAL_MACHINE, uninstallKey)
}

// stopProcessesUnder terminates every process whose executable lives under
// dir — the GUI and the launcher, which an install or uninstall must not
// leave holding files. The service itself is stopped through the SCM first;
// this process is skipped.
func stopProcessesUnder(dir string) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return
	}
	defer windows.CloseHandle(snap)
	prefix := strings.ToLower(filepath.Clean(dir)) + `\`
	self := uint32(os.Getpid())

	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		if e.ProcessID == self || e.ProcessID == 0 {
			continue
		}
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, e.ProcessID)
		if err != nil {
			continue
		}
		buf := make([]uint16, windows.MAX_PATH)
		n := uint32(len(buf))
		if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) == nil &&
			strings.HasPrefix(strings.ToLower(windows.UTF16ToString(buf[:n])), prefix) {
			if windows.TerminateProcess(h, 1) == nil {
				_, _ = windows.WaitForSingleObject(h, 5000)
			}
		}
		windows.CloseHandle(h)
	}
}

// removeDirLater deletes dir once this process has exited — the uninstaller
// runs from inside it and cannot delete its own executable.
//
// The command line goes to cmd.exe verbatim (CmdLine): Go's own quoting
// escapes inner quotes as \", which cmd.exe does not understand, and the
// quoted path would reach rmdir cut in pieces. rmdir is retried for about a
// minute, since a just-exited executable can stay open for a while (an
// antivirus scanning it).
//
// Deliberately NOT queued for deletion at reboot as a fallback: a reinstall
// before that reboot would have Windows delete the NEW install's files.
func removeDirLater(dir string) error {
	script := fmt.Sprintf(
		`ping -n 3 127.0.0.1 >nul & for /l %%i in (1,1,30) do @(rmdir /s /q "%[1]s" 2>nul & if not exist "%[1]s" exit /b 0 & ping -n 3 127.0.0.1 >nul)`,
		dir)
	cmd := exec.Command(filepath.Join(os.Getenv("WINDIR"), "System32", "cmd.exe"))
	cmd.Dir = os.TempDir() // never inside dir: a working directory pins it
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine:       `cmd.exe /d /s /c "` + script + `"`,
		HideWindow:    true,
		CreationFlags: createNoWindow | windows.DETACHED_PROCESS,
	}
	return cmd.Start()
}

// LaunchUnelevated starts exe with the desktop user's normal rights, from an
// elevated installer: explorer.exe runs unelevated and starts what it is
// handed with its own token. Starting the app directly would give it ours.
func LaunchUnelevated(exe string) error {
	return exec.Command(filepath.Join(os.Getenv("WINDIR"), "explorer.exe"), exe).Start()
}

func powershell(script string) (string, error) {
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// psQuote makes s a single-quoted PowerShell literal.
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func dirSize(dir string) int64 {
	var total int64
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}
