//go:build windows

// HikkariSetup.exe — the one file a user downloads. It carries the whole
// release bundle (payload/hikkari.zip, embedded at build time) and installs
// it through winsvc.Install: one UAC prompt, Program Files\Hikkari, the
// service, shortcuts, the "Apps & features" entry.
//
//	HikkariSetup.exe [--import DIR] [--quiet]
//
// --import names a portable install whose profiles to bring along; --quiet
// shows no dialogs and does not start the app (CI, scripted installs).
package main

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"

	"NekkoVPN/core/winsvc"
)

//go:embed payload
var payload embed.FS

func main() {
	args := os.Args[1:]
	for _, a := range args {
		if a == "--quiet" {
			winsvc.Quiet = true
		}
	}
	if !winsvc.IsElevated() {
		if err := winsvc.RelaunchElevated(args); err != nil {
			winsvc.Report(false, "Не удалось запросить права администратора: "+err.Error())
		}
		return
	}

	bundle, err := payload.ReadFile("payload/hikkari.zip")
	if err != nil {
		winsvc.Report(false, "Этот установщик собран без файлов приложения.")
		return
	}
	dest, err := winsvc.InstallDir()
	if err != nil {
		winsvc.Report(false, err.Error())
		return
	}
	// Unpacked beside the install directory, under Program Files, and not
	// in %TEMP%: between unpacking and copying, anything running as this
	// user (unelevated) could swap a binary in %TEMP% — which the service
	// would then run as SYSTEM. Program Files is writable by admins only.
	work := filepath.Join(filepath.Dir(dest), "Hikkari.setup")
	_ = os.RemoveAll(work)
	defer os.RemoveAll(work)
	if err := unzip(bundle, work); err != nil {
		winsvc.Report(false, "Распаковка: "+err.Error())
		return
	}

	launcher, err := winsvc.Install(filepath.Join(work, "Hikkari"), winsvc.ImportDirs(args))
	if err != nil {
		winsvc.Report(false, "Установка: "+err.Error())
		return
	}
	if winsvc.Quiet {
		fmt.Println("installed:", launcher)
		return
	}
	if winsvc.Ask("Hikkari VPN установлен.\n\nЗапустить сейчас?") {
		if err := winsvc.LaunchUnelevated(launcher); err != nil {
			winsvc.Report(false, "Запуск: "+err.Error())
		}
	}
}
