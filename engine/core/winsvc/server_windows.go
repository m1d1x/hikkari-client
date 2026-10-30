//go:build windows

// Package winsvc is HikkariService.exe on Windows: the SCM registration,
// installation and uninstallation around the platform-neutral service in
// core/service.
//
//	<root>\                        Program Files\Hikkari — admin-only writable
//	    Hikkari.exe                launcher — the shortcut points here
//	    HikkariService.exe         this service, LocalSystem
//	    hikkari-updater.exe
//	    app\                       GUI, engine DLL, sing-box.exe, wintun.dll
//	    core\                      service-owned: config.json, cache.db, service.log
//
// Everything the service executes or writes lives under <root>, which only
// administrators can modify.
package winsvc

import (
	"net"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/Microsoft/go-winio"

	vpn "NekkoVPN/core/engine/singbox/process"
	"NekkoVPN/core/service"
	"NekkoVPN/core/svcproto"
)

// pipeSDDL: SYSTEM and Administrators full control, interactively logged-on
// users read/write (that is the GUI), network logons denied outright.
const pipeSDDL = "D:P(D;;GA;;;NU)(A;;GA;;;SY)(A;;GA;;;BA)(A;;GRGW;;;IU)"

const createNoWindow = 0x08000000

// NewServer is the service for the installation at root.
func NewServer(root string) (*service.Server, error) {
	appDir := filepath.Join(root, "app")
	return service.NewServer(service.Config{
		CoreDir: filepath.Join(root, "core"),
		// The core runs from app\ (wintun.dll and libcronet.dll sit beside
		// it). app\ is replaced whole by an update, which is fine: the core
		// is stopped before that.
		CoreBin:     filepath.Join(appDir, vpn.BinaryName),
		CoreWorkDir: appDir,
		// The updater lives outside app\: Windows will not rename a
		// directory holding a running executable.
		Updater:     filepath.Join(root, "hikkari-updater.exe"),
		UpdaterArgs: []string{"--root", root, "--layout", "windows"},
		PrepareCmd: func(cmd *exec.Cmd) {
			cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
		},
		Listen: func() (net.Listener, error) {
			return winio.ListenPipe(svcproto.PipeName, &winio.PipeConfig{
				SecurityDescriptor: pipeSDDL,
				InputBufferSize:    64 << 10,
				OutputBufferSize:   64 << 10,
			})
		},
		Endpoint:  svcproto.PipeName,
		OnVersion: setDisplayVersion,
	})
}
