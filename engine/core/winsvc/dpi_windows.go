//go:build windows

package winsvc

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"NekkoVPN/core/netdoctor"
	"NekkoVPN/core/svcproto"
)

const (
	dpiBackupFile = "netreset-backup.json"
	// A service gets dpiServiceStopWait to stop, all of them dpiStopBudget:
	// the whole deep reset answers one pipe call the GUI is waiting on.
	dpiServiceStopWait = 8 * time.Second
	dpiStopBudget      = 30 * time.Second
	dpiProcessExitWait = 5 * time.Second
)

// dpiSystem is netdoctor.DPISystem on the real SCM and process list. Errors
// are bare Windows errors: service names and paths stay out of the logs.
type dpiSystem struct {
	coreDir  string
	deadline time.Time
}

func newDPISystem(coreDir string) *dpiSystem {
	return &dpiSystem{coreDir: coreDir, deadline: time.Now().Add(dpiStopBudget)}
}

// Services asks for drivers as well as Win32 services: WinDivert is a kernel
// driver, which svc/mgr.ListServices does not list.
func (d *dpiSystem) Services() ([]netdoctor.DPIService, error) {
	mgr, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT|windows.SC_MANAGER_ENUMERATE_SERVICE)
	if err != nil {
		return nil, fmt.Errorf("OpenSCManager: %w", err)
	}
	defer windows.CloseServiceHandle(mgr)

	var out []netdoctor.DPIService
	var resume uint32
	buf := make([]byte, 256<<10)
	for {
		var needed, count uint32
		err := windows.EnumServicesStatusEx(mgr, windows.SC_ENUM_PROCESS_INFO, windows.SERVICE_WIN32|windows.SERVICE_DRIVER,
			windows.SERVICE_STATE_ALL, &buf[0], uint32(len(buf)), &needed, &count, &resume, nil)
		if err != nil && !errors.Is(err, windows.ERROR_MORE_DATA) {
			return nil, fmt.Errorf("EnumServicesStatusEx: %w", err)
		}
		entries := unsafe.Slice((*windows.ENUM_SERVICE_STATUS_PROCESS)(unsafe.Pointer(&buf[0])), count)
		for i := range entries {
			// A service we may not read is skipped: without its binary it
			// cannot be recognized as a tool, and it is not ours to touch.
			if s, err := describeService(mgr, &entries[i]); err == nil {
				out = append(out, s)
			}
		}
		if err == nil {
			return out, nil
		}
		if count == 0 {
			if int(needed) <= len(buf) {
				return nil, errors.New("EnumServicesStatusEx: no progress")
			}
			buf = make([]byte, needed)
		}
	}
}

// selfPointingBuffer is memory for an API that stores, inside the buffer,
// pointers to its own strings. On the heap, where nothing moves it: a local
// make would stay on the goroutine stack, and when the stack grows the copy's
// pointers still lead into the old, freed one, which the runtime does not fix
// up in a byte slice.
//
//go:noinline
func selfPointingBuffer(size uint32) []byte { return make([]byte, size) }

func describeService(mgr windows.Handle, e *windows.ENUM_SERVICE_STATUS_PROCESS) (netdoctor.DPIService, error) {
	h, err := windows.OpenService(mgr, e.ServiceName, windows.SERVICE_QUERY_CONFIG)
	if err != nil {
		return netdoctor.DPIService{}, err
	}
	defer windows.CloseServiceHandle(h)

	buf := selfPointingBuffer(8 << 10)
	var needed uint32
	err = windows.QueryServiceConfig(h, (*windows.QUERY_SERVICE_CONFIG)(unsafe.Pointer(&buf[0])), uint32(len(buf)), &needed)
	if errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) {
		buf = selfPointingBuffer(needed)
		err = windows.QueryServiceConfig(h, (*windows.QUERY_SERVICE_CONFIG)(unsafe.Pointer(&buf[0])), uint32(len(buf)), &needed)
	}
	if err != nil {
		return netdoctor.DPIService{}, err
	}
	cfg := (*windows.QUERY_SERVICE_CONFIG)(unsafe.Pointer(&buf[0]))
	s := netdoctor.DPIService{
		Name:      windows.UTF16PtrToString(e.ServiceName),
		ImagePath: windows.UTF16PtrToString(cfg.BinaryPathName),
		Driver:    cfg.ServiceType&windows.SERVICE_DRIVER != 0,
		StartType: cfg.StartType,
		Running:   e.ServiceStatusProcess.CurrentState != windows.SERVICE_STOPPED,
	}
	if cfg.StartType == windows.SERVICE_AUTO_START {
		var delayed windows.SERVICE_DELAYED_AUTO_START_INFO
		if err := windows.QueryServiceConfig2(h, windows.SERVICE_CONFIG_DELAYED_AUTO_START_INFO,
			(*byte)(unsafe.Pointer(&delayed)), uint32(unsafe.Sizeof(delayed)), &needed); err == nil {
			s.DelayedAutoStart = delayed.IsDelayedAutoStartUp != 0
		}
	}
	return s, nil
}

// withService runs fn on the named service. A service that no longer exists
// has nothing left to do: WinDivert removes its own driver service when the
// last tool using it exits.
func withService(name string, access uint32, fn func(windows.Handle) error) error {
	mgr, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return fmt.Errorf("OpenSCManager: %w", err)
	}
	defer windows.CloseServiceHandle(mgr)
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return errors.New("bad service name")
	}
	h, err := windows.OpenService(mgr, namePtr, access)
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("OpenService: %w", err)
	}
	defer windows.CloseServiceHandle(h)
	return fn(h)
}

func (d *dpiSystem) SetManualStart(name string) error {
	return withService(name, windows.SERVICE_CHANGE_CONFIG, func(h windows.Handle) error {
		err := windows.ChangeServiceConfig(h, windows.SERVICE_NO_CHANGE, windows.SERVICE_DEMAND_START, windows.SERVICE_NO_CHANGE,
			nil, nil, nil, nil, nil, nil, nil)
		// Marked for deletion: it goes away with the last handle and will
		// not start again either way.
		if err != nil && !errors.Is(err, windows.ERROR_SERVICE_MARKED_FOR_DELETE) {
			return fmt.Errorf("ChangeServiceConfig: %w", err)
		}
		return nil
	})
}

func (d *dpiSystem) StopService(name string) error {
	return withService(name, windows.SERVICE_STOP|windows.SERVICE_QUERY_STATUS, func(h windows.Handle) error {
		var st windows.SERVICE_STATUS
		err := windows.ControlService(h, windows.SERVICE_CONTROL_STOP, &st)
		switch {
		case errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE):
			return nil
		case err != nil && !errors.Is(err, windows.ERROR_SERVICE_CANNOT_ACCEPT_CTRL): // the latter: already stopping
			return fmt.Errorf("ControlService: %w", err)
		}
		wait := time.Now().Add(dpiServiceStopWait)
		if d.deadline.Before(wait) {
			wait = d.deadline
		}
		for {
			if err := windows.QueryServiceStatus(h, &st); err != nil {
				return fmt.Errorf("QueryServiceStatus: %w", err)
			}
			if st.CurrentState == windows.SERVICE_STOPPED {
				return nil
			}
			if time.Now().After(wait) {
				return errors.New("service did not stop in time")
			}
			time.Sleep(200 * time.Millisecond)
		}
	})
}

func (d *dpiSystem) Processes() ([]netdoctor.DPIProcess, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, fmt.Errorf("CreateToolhelp32Snapshot: %w", err)
	}
	defer windows.CloseHandle(snap)

	var out []netdoctor.DPIProcess
	e := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		out = append(out, netdoctor.DPIProcess{PID: e.ProcessID, Path: windows.UTF16ToString(e.ExeFile[:])})
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return nil, fmt.Errorf("Process32Next: %w", err)
	}
	return out, nil
}

// KillProcess re-reads the image name through the open handle: the handle
// pins the process, so the check and the kill are about the same one even
// if the PID from the listing has been reused.
func (d *dpiSystem) KillProcess(pid uint32, class string) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, pid)
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
	if netdoctor.ClassifyDPIProcess(windows.UTF16ToString(name[:size])) != class {
		return nil
	}
	termErr := windows.TerminateProcess(h, 1)
	event, err := windows.WaitForSingleObject(h, uint32(dpiProcessExitWait/time.Millisecond))
	if err == nil && event == windows.WAIT_OBJECT_0 {
		return nil
	}
	if termErr != nil {
		return fmt.Errorf("TerminateProcess: %w", termErr)
	}
	return errors.New("process did not exit in time")
}

// winDivertState reports the WinDivert driver and who has it loaded. It says
// nil, nil on a machine without the driver.
func winDivertState(d *dpiSystem) (*svcproto.WinDivertState, error) {
	services, err := d.Services()
	if err != nil {
		return nil, err
	}
	state := netdoctor.WinDivertDriver(services)
	if state == nil {
		return nil, nil
	}
	processes, err := d.Processes()
	if err != nil {
		return state, err
	}
	state.Holders = []string{}
	for _, p := range processes {
		if processLoadsWinDivert(p.PID) {
			state.Holders = append(state.Holders, netdoctor.ProcessLabel(p.Path))
		}
	}
	return state, nil
}

// processLoadsWinDivert looks through the process's modules. A process that
// may not be read (protected, gone since the listing) counts as not holding.
func processLoadsWinDivert(pid uint32) bool {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPMODULE|windows.TH32CS_SNAPMODULE32, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(snap)
	e := windows.ModuleEntry32{Size: uint32(unsafe.Sizeof(windows.ModuleEntry32{}))}
	for err = windows.Module32First(snap, &e); err == nil; err = windows.Module32Next(snap, &e) {
		if netdoctor.IsWinDivertModule(windows.UTF16ToString(e.Module[:])) {
			return true
		}
	}
	return false
}

func (d *dpiSystem) ReadBackup() ([]byte, error) {
	content, err := os.ReadFile(filepath.Join(d.coreDir, dpiBackupFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, netdoctor.BareFileError(err)
	}
	return content, nil
}

func (d *dpiSystem) WriteBackup(content []byte) error {
	path := filepath.Join(d.coreDir, dpiBackupFile)
	return netdoctor.BareFileError(netdoctor.ReplaceFile(path, content, 0o600))
}
