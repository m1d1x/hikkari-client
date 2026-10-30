//go:build windows

// HikkariService.exe — the privileged half of the Windows client. See
// core/winsvc.
//
//	HikkariService.exe install [--import DIR] [--quiet]   copy this build to Program Files, register, start
//	HikkariService.exe uninstall [--quiet]                stop and remove the service
//	HikkariService.exe debug                              serve in the foreground (elevated shell)
//
// Started by the SCM it takes no arguments. install and uninstall re-launch
// themselves elevated (one UAC prompt) when needed and report the outcome in
// a message box, since the elevated console closes when they finish;
// --quiet (CI, scripted installs) reports on stdout and the exit code only.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"

	"NekkoVPN/core/winsvc"
)

func main() {
	if isSvc, err := svc.IsWindowsService(); err == nil && isSvc {
		if err := winsvc.RunService(); err != nil {
			os.Exit(1)
		}
		return
	}

	args := os.Args[1:]
	if len(args) == 0 {
		args = []string{"install"} // double-clicked
	}
	for _, a := range args[1:] {
		if a == "--quiet" {
			quiet = true
		}
	}
	switch args[0] {
	case "install", "uninstall":
		if !winsvc.IsElevated() {
			if err := relaunchElevated(args); err != nil {
				report(false, "Не удалось запросить права администратора: "+err.Error())
			}
			return
		}
		if args[0] == "uninstall" {
			if err := winsvc.Uninstall(); err != nil {
				report(false, "Удаление службы: "+err.Error())
				return
			}
			report(true, "Служба Hikkari удалена.")
			return
		}
		src, err := os.Executable()
		if err != nil {
			report(false, err.Error())
			return
		}
		gui, err := winsvc.Install(filepath.Dir(src), importDirs(args[1:]))
		if err != nil {
			report(false, "Установка: "+err.Error())
			return
		}
		report(true, "Hikkari установлен.\n\nЗапуск: "+gui)
	case "debug":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := winsvc.RunConsole(ctx); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, "usage: HikkariService.exe install [--import DIR] | uninstall | debug")
		os.Exit(2)
	}
}

func importDirs(args []string) []string {
	var dirs []string
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--import" {
			dirs = append(dirs, args[i+1])
		}
	}
	return dirs
}

func relaunchElevated(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = syscall.EscapeArg(a)
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	params, _ := windows.UTF16PtrFromString(strings.Join(quoted, " "))
	dir, _ := windows.UTF16PtrFromString(filepath.Dir(exe))
	return windows.ShellExecute(0, verb, file, params, dir, windows.SW_NORMAL)
}

// quiet suppresses the message box (see --quiet).
var quiet bool

func report(ok bool, text string) {
	fmt.Println(text)
	if quiet {
		if !ok {
			os.Exit(1)
		}
		return
	}
	flags := uint32(windows.MB_OK | windows.MB_ICONINFORMATION)
	if !ok {
		flags = windows.MB_OK | windows.MB_ICONERROR
	}
	t, _ := windows.UTF16PtrFromString(text)
	c, _ := windows.UTF16PtrFromString("Hikkari")
	_, _ = windows.MessageBox(0, t, c, flags)
	if !ok {
		os.Exit(1)
	}
}
