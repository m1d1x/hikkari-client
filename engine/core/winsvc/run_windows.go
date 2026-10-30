//go:build windows

package winsvc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"

	"NekkoVPN/core/svcproto"
)

// RunService is the entry point when the SCM starts us.
func RunService() error {
	root, err := installedRoot()
	if err != nil {
		return err
	}
	return svc.Run(svcproto.ServiceName, &handler{root: root})
}

// RunConsole runs the same server in the foreground, for development from an
// elevated shell. No root check: whoever runs it is already an administrator.
func RunConsole(ctx context.Context) error {
	root, err := exeDir()
	if err != nil {
		return err
	}
	srv, err := NewServer(root)
	if err != nil {
		return err
	}
	fmt.Printf("HikkariService (console) root=%s, log=%s\n", root, srv.log.Path())
	return srv.Serve(ctx)
}

type handler struct{ root string }

func (h *handler) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	srv, err := NewServer(h.root)
	if err != nil {
		return false, 1
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()

	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case req := <-requests:
			switch req.Cmd {
			case svc.Interrogate:
				status <- req.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				<-done
				return false, 0
			}
		case err := <-done:
			// The pipe could not be served at all; let the SCM's recovery
			// actions restart us.
			cancel()
			if err != nil {
				return false, 2
			}
			return false, 0
		}
	}
}

func exeDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("finding this executable: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", err
	}
	return filepath.Dir(exe), nil
}

// installedRoot is the service's directory, provided it is the one under
// Program Files. A LocalSystem service executing binaries from a folder an
// ordinary user can write to would hand SYSTEM to that user, so the service
// refuses to run from anywhere else.
func installedRoot() (string, error) {
	root, err := exeDir()
	if err != nil {
		return "", err
	}
	want, err := InstallDir()
	if err != nil {
		return "", err
	}
	if !samePath(root, want) {
		return "", fmt.Errorf("the service runs only from %s, not %s", want, root)
	}
	return root, nil
}

// InstallDir is %ProgramFiles%\Hikkari.
func InstallDir() (string, error) {
	pf, err := windows.KnownFolderPath(windows.FOLDERID_ProgramFiles, 0)
	if err != nil {
		return "", fmt.Errorf("locating Program Files: %w", err)
	}
	return filepath.Join(pf, "Hikkari"), nil
}

func samePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}
