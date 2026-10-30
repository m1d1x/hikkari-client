//go:build windows

// HikkariService.exe — the privileged half of the Windows client. See
// core/winsvc.
//
//	HikkariService.exe uninstall [--quiet]                remove Hikkari ("Apps & features" runs this)
//	HikkariService.exe install [--import DIR] [--quiet]   install from an unpacked build (development;
//	                                                      users run HikkariSetup.exe)
//	HikkariService.exe debug                              serve in the foreground (elevated shell)
//
// Started by the SCM it takes no arguments. install and uninstall re-launch
// themselves elevated (one UAC prompt) when needed.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

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
		fmt.Fprintln(os.Stderr, "usage: HikkariService.exe uninstall | install [--import DIR] | debug  [--quiet]")
		os.Exit(2)
	}
	for _, a := range args[1:] {
		if a == "--quiet" {
			winsvc.Quiet = true
		}
	}
	switch args[0] {
	case "install", "uninstall":
		if !winsvc.IsElevated() {
			if err := winsvc.RelaunchElevated(args); err != nil {
				winsvc.Report(false, "Не удалось запросить права администратора: "+err.Error())
			}
			return
		}
		if args[0] == "uninstall" {
			uninstall()
			return
		}
		src, err := os.Executable()
		if err != nil {
			winsvc.Report(false, err.Error())
			return
		}
		launcher, err := winsvc.Install(filepath.Dir(src), winsvc.ImportDirs(args[1:]))
		if err != nil {
			winsvc.Report(false, "Установка: "+err.Error())
			return
		}
		winsvc.Report(true, "Hikkari установлен.\n\nЗапуск: "+launcher)
	case "debug":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := winsvc.RunConsole(ctx); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, "unknown command", args[0])
		os.Exit(2)
	}
}

func uninstall() {
	if !winsvc.Ask("Удалить Hikkari VPN?\n\nПрофили и настройки останутся в %LOCALAPPDATA%\\Hikkari.") {
		return
	}
	if err := winsvc.Uninstall(); err != nil {
		winsvc.Report(false, "Удаление: "+err.Error())
		return
	}
	winsvc.Report(true, "Hikkari удалён.")
}
