//go:build windows

package winsvc

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/sys/windows"

	"NekkoVPN/core/foreignvpn"
)

const (
	// A tunnel service gets foreignServiceStopWait to stop: WireGuard's
	// tunnel service removes its adapter and routes on the way out.
	foreignServiceStopWait = 10 * time.Second
	foreignProcessExitWait = 5 * time.Second
)

// foreignSystem is foreignvpn.System on the SCM and the process list.
// Windows VPN configurations of the system itself (RAS) are not managed.
type foreignSystem struct{}

func (foreignSystem) Processes() ([]foreignvpn.Process, error) {
	procs, err := (&dpiSystem{}).Processes()
	if err != nil {
		return nil, err
	}
	out := make([]foreignvpn.Process, 0, len(procs))
	for _, p := range procs {
		out = append(out, foreignvpn.Process{PID: p.PID, Path: p.Path})
	}
	return out, nil
}

func (foreignSystem) Services() ([]foreignvpn.Service, error) {
	services, err := (&dpiSystem{}).Services()
	if err != nil {
		return nil, err
	}
	out := make([]foreignvpn.Service, 0, len(services))
	for _, s := range services {
		if !s.Driver {
			out = append(out, foreignvpn.Service{Name: s.Name, Running: s.Running})
		}
	}
	return out, nil
}

func (foreignSystem) StopService(name string) error {
	return (&dpiSystem{deadline: time.Now().Add(foreignServiceStopWait)}).StopService(name)
}

func (foreignSystem) ConnectedVPNs() ([]foreignvpn.SystemVPN, error) { return nil, nil }

func (foreignSystem) StopVPN(foreignvpn.SystemVPN) error { return nil }

// KillProcess re-reads the image name through the open handle, which pins
// the process: the check and the kill are about the same one even if the
// PID from the listing has been reused. The listing has file names only, so
// the file names are compared.
func (foreignSystem) KillProcess(p foreignvpn.Process) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, p.PID)
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return nil // already gone
	}
	if err != nil {
		return fmt.Errorf("OpenProcess: %w", err)
	}
	defer windows.CloseHandle(h)

	name := make([]uint16, windows.MAX_LONG_PATH)
	size := uint32(len(name))
	if err := windows.QueryFullProcessImageName(h, 0, &name[0], &size); err != nil {
		return fmt.Errorf("QueryFullProcessImageName: %w", err)
	}
	if !strings.EqualFold(fileName(windows.UTF16ToString(name[:size])), fileName(p.Path)) {
		return nil
	}
	termErr := windows.TerminateProcess(h, 1)
	event, err := windows.WaitForSingleObject(h, uint32(foreignProcessExitWait/time.Millisecond))
	if err == nil && event == windows.WAIT_OBJECT_0 {
		return nil
	}
	if termErr != nil {
		return fmt.Errorf("TerminateProcess: %w", termErr)
	}
	return errors.New("process did not exit in time")
}

func fileName(path string) string {
	if i := strings.LastIndexAny(path, `\/`); i >= 0 {
		return path[i+1:]
	}
	return path
}
