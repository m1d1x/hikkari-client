package netdoctor

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"NekkoVPN/core/svcproto"
)

// The DPI bypass tools the deep reset stops, as they appear in logs and in
// the service protocol: a class, never a service name (those are free text
// chosen by whoever installed the tool) or a path.
const (
	DPIClassWinws      = "winws"
	DPIClassGoodbyeDPI = "goodbyedpi"
	DPIClassWinDivert  = "windivert"
)

// Service start types, the Windows SERVICE_*_START values.
const (
	startBoot   = 0
	startSystem = 1
	startAuto   = 2
)

// ClassifyDPIProcess names the tool an executable path or file name belongs
// to, "" for anything else.
func ClassifyDPIProcess(exe string) string {
	return dpiToolClass(pathBase(exe))
}

// ClassifyDPIService names the tool a service runs, judged by its binary
// alone: a folder or an argument that mentions a tool proves nothing, and a
// wrapper such as cmd.exe is not on the list. WinDivert counts only as a
// kernel driver, the one thing that holds the packet filter.
func ClassifyDPIService(imagePath string, driver bool) string {
	base := pathBase(serviceBinary(imagePath))
	if driver {
		if strings.Contains(base, DPIClassWinDivert) {
			return DPIClassWinDivert
		}
		return ""
	}
	return dpiToolClass(base)
}

func dpiToolClass(base string) string {
	switch strings.TrimSuffix(base, ".exe") {
	case DPIClassWinws:
		return DPIClassWinws
	case DPIClassGoodbyeDPI:
		return DPIClassGoodbyeDPI
	}
	return ""
}

// pathBase is the lower-cased last element of a Windows path. path/filepath
// would not split on a backslash when built for tests on other systems.
func pathBase(path string) string {
	if i := strings.LastIndexAny(path, `\/`); i >= 0 {
		path = path[i+1:]
	}
	return strings.ToLower(path)
}

// serviceBinary cuts the executable out of a service's ImagePath: quoted, or
// unquoted and possibly with spaces in the path, and followed by arguments.
func serviceBinary(imagePath string) string {
	s := strings.TrimSpace(imagePath)
	if rest, quoted := strings.CutPrefix(s, `"`); quoted {
		bin, _, _ := strings.Cut(rest, `"`)
		return bin
	}
	for end := 4; end <= len(s); end++ {
		ext := s[end-4 : end]
		if !strings.EqualFold(ext, ".exe") && !strings.EqualFold(ext, ".sys") {
			continue
		}
		if end == len(s) || s[end] == ' ' || s[end] == '\t' {
			return s[:end]
		}
	}
	if fields := strings.Fields(s); len(fields) > 0 {
		return fields[0]
	}
	return ""
}

// DPIService is one installed service as the OS reports it.
type DPIService struct {
	Name             string
	ImagePath        string
	Driver           bool
	StartType        uint32
	DelayedAutoStart bool
	Running          bool
}

// DPIProcess is one running process.
type DPIProcess struct {
	PID  uint32
	Path string
}

// DPISystem is the OS side of stopping DPI tools. Its errors go to the log,
// so they carry neither service names nor paths.
type DPISystem interface {
	// Services lists every installed service and driver.
	Services() ([]DPIService, error)
	// Processes lists the running processes.
	Processes() ([]DPIProcess, error)
	// SetManualStart changes the start type and nothing else.
	SetManualStart(name string) error
	// StopService stops the service and waits for it. A service that is
	// already gone is stopped.
	StopService(name string) error
	// KillProcess ends pid if it still is a process of that class: the PID
	// may have been reused since the listing.
	KillProcess(pid uint32, class string) error
	// ReadBackup returns the backup file, nil when there is none.
	ReadBackup() ([]byte, error)
	// WriteBackup replaces the backup file atomically.
	WriteBackup(content []byte) error
}

// DPIBackup is core\netreset-backup.json: what a service's start type was
// before the deep reset changed it.
type DPIBackup struct {
	Services []DPIBackupEntry `json:"services"`
}

// DPIBackupEntry records one service's original start type.
type DPIBackupEntry struct {
	Name             string `json:"name"`
	Class            string `json:"class"`
	StartType        uint32 `json:"startType"`
	DelayedAutoStart bool   `json:"delayedAutoStart,omitempty"`
}

// merge records the entries, replacing those of the same service (SCM names
// are case-insensitive). It sees only services about to change, whose
// current start type is the real original: one a previous reset made manual
// is not among them, so its entry survives any number of resets.
func (b *DPIBackup) merge(entries []DPIBackupEntry) {
	for _, e := range entries {
		i := slices.IndexFunc(b.Services, func(have DPIBackupEntry) bool {
			return strings.EqualFold(have.Name, e.Name)
		})
		if i >= 0 {
			b.Services[i] = e
		} else {
			b.Services = append(b.Services, e)
		}
	}
}

type dpiTarget struct {
	DPIService
	class string
}

// startsByItself: left alone, the service comes back at the next boot. A
// disabled service stays disabled — manual would be a step up for it.
func (s DPIService) startsByItself() bool {
	return s.StartType == startBoot || s.StartType == startSystem || s.StartType == startAuto
}

// dpiTargets are the services of a known tool that need action. A stopped
// manual or disabled one neither filters packets nor will come back.
func dpiTargets(services []DPIService) []dpiTarget {
	var out []dpiTarget
	for _, s := range services {
		class := ClassifyDPIService(s.ImagePath, s.Driver)
		if class != "" && (s.Running || s.startsByItself()) {
			out = append(out, dpiTarget{s, class})
		}
	}
	return out
}

// ScanDPITools reports, read-only, the classes the deep reset would act on.
// The result is non-nil on success, so "none found" differs from "unknown".
func ScanDPITools(sys DPISystem) ([]string, error) {
	services, err := sys.Services()
	if err != nil {
		return nil, err
	}
	processes, err := sys.Processes()
	if err != nil {
		return nil, err
	}
	found := []string{}
	for _, t := range dpiTargets(services) {
		found = append(found, t.class)
	}
	for _, p := range processes {
		if class := ClassifyDPIProcess(p.Path); class != "" {
			found = append(found, class)
		}
	}
	return classList(found), nil
}

// DPIOutcome is what StopDPITools did. A class can be in both lists when one
// of its services stopped and another did not.
type DPIOutcome struct {
	// Stopped: at least one service or process of the class was stopped or
	// no longer starts by itself.
	Stopped []string
	// Failed: something of the class is still running or still starts by
	// itself.
	Failed []string
	// Errors say why, by class.
	Errors []string
}

func (o *DPIOutcome) fail(class string, err error) {
	o.Failed = append(o.Failed, class)
	o.Errors = append(o.Errors, fmt.Sprintf("%s: %v", class, err))
}

// StopDPITools stops the known DPI tools: only stop and manual start, never
// removal. The returned error means nothing was touched: the services could
// not be listed or the original start types could not be saved, and without
// that backup the change could not be undone.
//
// The order matters: user-mode services first, then stray processes, and the
// WinDivert driver last, because it cannot stop while they hold it open.
func StopDPITools(sys DPISystem) (DPIOutcome, error) {
	var out DPIOutcome
	services, err := sys.Services()
	if err != nil {
		return out, fmt.Errorf("listing services: %w", err)
	}
	targets := dpiTargets(services)
	if err := backupStartTypes(sys, targets); err != nil {
		return out, err
	}

	stopService := func(t dpiTarget) {
		acted := false
		if t.startsByItself() {
			if err := sys.SetManualStart(t.Name); err != nil {
				out.fail(t.class, err)
			} else {
				acted = true
			}
		}
		if t.Running {
			if err := sys.StopService(t.Name); err != nil {
				out.fail(t.class, err)
			} else {
				acted = true
			}
		}
		if acted {
			out.Stopped = append(out.Stopped, t.class)
		}
	}
	for _, t := range targets {
		if !t.Driver {
			stopService(t)
		}
	}

	// Listed only now: stopping the services has ended their processes.
	if processes, err := sys.Processes(); err != nil {
		out.fail(DPIClassWinws, err)
		out.fail(DPIClassGoodbyeDPI, err)
	} else {
		for _, p := range processes {
			class := ClassifyDPIProcess(p.Path)
			if class == "" {
				continue
			}
			if err := sys.KillProcess(p.PID, class); err != nil {
				out.fail(class, err)
			} else {
				out.Stopped = append(out.Stopped, class)
			}
		}
	}

	for _, t := range targets {
		if t.Driver {
			stopService(t)
		}
	}
	out.Stopped, out.Failed = classList(out.Stopped), classList(out.Failed)
	return out, nil
}

// backupStartTypes saves the start types about to change. An unreadable
// backup is an error, not a fresh start: it may hold the only record of a
// previous reset's originals.
func backupStartTypes(sys DPISystem, targets []dpiTarget) error {
	var entries []DPIBackupEntry
	for _, t := range targets {
		if t.startsByItself() {
			entries = append(entries, DPIBackupEntry{
				Name:             t.Name,
				Class:            t.class,
				StartType:        t.StartType,
				DelayedAutoStart: t.DelayedAutoStart,
			})
		}
	}
	if len(entries) == 0 {
		return nil
	}
	var backup DPIBackup
	content, err := sys.ReadBackup()
	if err != nil {
		return fmt.Errorf("reading start type backup: %w", err)
	}
	if content != nil {
		if err := json.Unmarshal(content, &backup); err != nil {
			return fmt.Errorf("start type backup is damaged: %w", err)
		}
	}
	backup.merge(entries)
	content, err = json.MarshalIndent(backup, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding start type backup: %w", err)
	}
	if err := sys.WriteBackup(content); err != nil {
		return fmt.Errorf("writing start type backup: %w", err)
	}
	return nil
}

// classList sorts and dedupes, keeping a non-nil list non-nil.
func classList(classes []string) []string {
	slices.Sort(classes)
	return slices.Compact(classes)
}

// WinDivertDriver reports the installed WinDivert driver, nil if there is none.
// Holders are left for the caller, who can read other processes.
func WinDivertDriver(services []DPIService) *svcproto.WinDivertState {
	var state *svcproto.WinDivertState
	for _, s := range services {
		if ClassifyDPIService(s.ImagePath, s.Driver) != DPIClassWinDivert {
			continue
		}
		if state == nil {
			state = &svcproto.WinDivertState{}
		}
		state.Running = state.Running || s.Running
	}
	return state
}

// IsWinDivertModule recognizes the library programs use the driver through.
func IsWinDivertModule(name string) bool {
	return strings.Contains(strings.ToLower(name), DPIClassWinDivert)
}

// ProcessLabel is an executable's name, safe for one log line: the holder of a
// driver is any program at all, and its folder may name the user.
func ProcessLabel(path string) string { return cleanProviderName(pathBase(path)) }
