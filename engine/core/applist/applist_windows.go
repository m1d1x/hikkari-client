package applist

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

var (
	modKernel32 = syscall.NewLazyDLL("kernel32.dll")
	modUser32   = syscall.NewLazyDLL("user32.dll")
	modVersion  = syscall.NewLazyDLL("version.dll")

	procCreateToolhelp32Snapshot   = modKernel32.NewProc("CreateToolhelp32Snapshot")
	procProcess32FirstW            = modKernel32.NewProc("Process32FirstW")
	procProcess32NextW             = modKernel32.NewProc("Process32NextW")
	procOpenProcess                = modKernel32.NewProc("OpenProcess")
	procCloseHandle                = modKernel32.NewProc("CloseHandle")
	procQueryFullProcessImageNameW = modKernel32.NewProc("QueryFullProcessImageNameW")

	procEnumWindows              = modUser32.NewProc("EnumWindows")
	procGetWindowThreadProcessId = modUser32.NewProc("GetWindowThreadProcessId")
	procIsWindowVisible          = modUser32.NewProc("IsWindowVisible")
	procGetWindowTextLengthW     = modUser32.NewProc("GetWindowTextLengthW")

	procGetFileVersionInfoSizeW = modVersion.NewProc("GetFileVersionInfoSizeW")
	procGetFileVersionInfoW     = modVersion.NewProc("GetFileVersionInfoW")
	procVerQueryValueW          = modVersion.NewProc("VerQueryValueW")
)

const (
	tH32CS_SNAPPROCESS      = 0x00000002
	processQueryLimitedInfo = 0x1000
	maxPath                 = 260
)

type processEntry32 struct {
	Size            uint32
	Usage           uint32
	ProcessID       uint32
	DefaultHeapID   uintptr
	ModuleID        uint32
	Threads         uint32
	ParentProcessID uint32
	PriClassBase    int32
	Flags           uint32
	ExeFile         [maxPath]uint16
}

// List returns running applications, deduplicated by executable name.
//
// all=false is the short list the picker opens with: processes that own a
// visible window, minus the system shell and the helper/updater noise. That
// misses anything headless — a game's anti-cheat service, a launcher's
// network helper — which is exactly what someone splitting routing per app
// usually needs to reach. all=true therefore drops both filters and returns
// every process in the snapshot, windowed or not; Running then tells the two
// apart so the UI can still show which ones have a window.
func List(all bool) []AppEntry {
	windowedPIDs := enumWindowedPIDs()
	procs := snapshotProcesses()

	seen := map[string]bool{}
	var result []AppEntry

	for _, p := range procs {
		exeLower := strings.ToLower(p.exe)
		if seen[exeLower] {
			continue
		}
		windowed := windowedPIDs[p.pid]
		if !all {
			if !windowed {
				continue
			}
			if isSystemProcess(exeLower) {
				continue
			}
		}

		fullPath := queryFullPath(p.pid)
		if fullPath == "" {
			fullPath = p.exe
		}

		m := metaFor(fullPath)
		name := m.name
		if name == "" {
			name = strings.TrimSuffix(p.exe, filepath.Ext(p.exe))
		}

		seen[exeLower] = true
		result = append(result, AppEntry{
			Name:    name,
			Exe:     p.exe,
			Path:    fullPath,
			Running: windowed,
			Icon:    m.icon,
		})
	}

	sort.Slice(result, func(i, j int) bool {
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return result
}

// exeMeta is the per-binary data that costs real work to obtain: the version
// resource read for the friendly name and the shell icon extraction plus PNG
// encode. Neither changes while the file sits on disk, and the picker polls
// every few seconds over a list that with all=true runs to a few hundred
// processes — so both are cached by path for the lifetime of the process.
type exeMeta struct {
	name string
	icon string
}

var (
	metaMu    sync.Mutex
	metaCache = map[string]exeMeta{}
)

func metaFor(exePath string) exeMeta {
	key := strings.ToLower(exePath)

	metaMu.Lock()
	m, ok := metaCache[key]
	metaMu.Unlock()
	if ok {
		return m
	}

	m = exeMeta{
		name: friendlyName(exePath),
		icon: extractIconBase64(exePath),
	}

	metaMu.Lock()
	metaCache[key] = m
	metaMu.Unlock()
	return m
}

type procInfo struct {
	pid uint32
	exe string
}

func snapshotProcesses() []procInfo {
	handle, _, _ := procCreateToolhelp32Snapshot.Call(tH32CS_SNAPPROCESS, 0)
	if handle == ^uintptr(0) {
		return nil
	}
	defer procCloseHandle.Call(handle)

	var entry processEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))

	ret, _, _ := procProcess32FirstW.Call(handle, uintptr(unsafe.Pointer(&entry)))
	if ret == 0 {
		return nil
	}

	var result []procInfo
	for {
		exe := syscall.UTF16ToString(entry.ExeFile[:])
		result = append(result, procInfo{pid: entry.ProcessID, exe: exe})

		entry.Size = uint32(unsafe.Sizeof(entry))
		ret, _, _ = procProcess32NextW.Call(handle, uintptr(unsafe.Pointer(&entry)))
		if ret == 0 {
			break
		}
	}
	return result
}

func enumWindowedPIDs() map[uint32]bool {
	pids := map[uint32]bool{}
	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		visible, _, _ := procIsWindowVisible.Call(hwnd)
		if visible == 0 {
			return 1
		}
		textLen, _, _ := procGetWindowTextLengthW.Call(hwnd)
		if textLen == 0 {
			return 1
		}
		var pid uint32
		procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		if pid != 0 {
			pids[pid] = true
		}
		return 1
	})
	procEnumWindows.Call(cb, 0)
	return pids
}

func queryFullPath(pid uint32) string {
	h, _, _ := procOpenProcess.Call(processQueryLimitedInfo, 0, uintptr(pid))
	if h == 0 {
		return ""
	}
	defer procCloseHandle.Call(h)

	buf := make([]uint16, 1024)
	size := uint32(len(buf))
	ret, _, _ := procQueryFullProcessImageNameW.Call(h, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if ret == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:size])
}

func friendlyName(exePath string) string {
	if exePath == "" || !strings.Contains(exePath, `\`) {
		return ""
	}
	pathPtr, _ := syscall.UTF16PtrFromString(exePath)

	var dummy uint32
	size, _, _ := procGetFileVersionInfoSizeW.Call(uintptr(unsafe.Pointer(pathPtr)), uintptr(unsafe.Pointer(&dummy)))
	if size == 0 {
		return ""
	}

	block := make([]byte, size)
	ret, _, _ := procGetFileVersionInfoW.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		0,
		size,
		uintptr(unsafe.Pointer(&block[0])),
	)
	if ret == 0 {
		return ""
	}

	// Try common translation code pages
	translations := []string{
		`\StringFileInfo\040904B0\`, // English, Unicode
		`\StringFileInfo\040904E4\`, // English, Latin-1
		`\StringFileInfo\041904B0\`, // Russian, Unicode
		`\StringFileInfo\000004B0\`, // Neutral, Unicode
	}

	for _, trans := range translations {
		if name := queryStringValue(block, trans+"ProductName"); name != "" {
			return name
		}
		if name := queryStringValue(block, trans+"FileDescription"); name != "" {
			return name
		}
	}

	// Fallback: enumerate actual translation table
	subBlock, _ := syscall.UTF16PtrFromString(`\VarFileInfo\Translation`)
	var transPtr *byte
	var transLen uint32
	ret, _, _ = procVerQueryValueW.Call(
		uintptr(unsafe.Pointer(&block[0])),
		uintptr(unsafe.Pointer(subBlock)),
		uintptr(unsafe.Pointer(&transPtr)),
		uintptr(unsafe.Pointer(&transLen)),
	)
	if ret != 0 && transLen >= 4 {
		type langCP struct {
			Lang uint16
			CP   uint16
		}
		lcp := (*langCP)(unsafe.Pointer(transPtr))
		prefix := fmt.Sprintf(`\StringFileInfo\%04X%04X\`, lcp.Lang, lcp.CP)
		if name := queryStringValue(block, prefix+"ProductName"); name != "" {
			return name
		}
		if name := queryStringValue(block, prefix+"FileDescription"); name != "" {
			return name
		}
	}

	return ""
}

func queryStringValue(block []byte, subBlock string) string {
	sub, _ := syscall.UTF16PtrFromString(subBlock)
	var valPtr *uint16
	var valLen uint32
	ret, _, _ := procVerQueryValueW.Call(
		uintptr(unsafe.Pointer(&block[0])),
		uintptr(unsafe.Pointer(sub)),
		uintptr(unsafe.Pointer(&valPtr)),
		uintptr(unsafe.Pointer(&valLen)),
	)
	if ret == 0 || valLen == 0 {
		return ""
	}
	buf := unsafe.Slice(valPtr, valLen)
	s := syscall.UTF16ToString(buf)
	return strings.TrimSpace(s)
}

func isSystemProcess(exeLower string) bool {
	skip := map[string]bool{
		"explorer.exe":                true,
		"shellexperiencehost.exe":     true,
		"searchhost.exe":              true,
		"startmenuexperiencehost.exe": true,
		"runtimebroker.exe":           true,
		"applicationframehost.exe":    true,
		"systemsettings.exe":          true,
		"textinputhost.exe":           true,
		"widgetservice.exe":           true,
		"widgets.exe":                 true,
		"lockapp.exe":                 true,
		"dwm.exe":                     true,
		"taskhostw.exe":               true,
		"ctfmon.exe":                  true,
		"securityhealthsystray.exe":   true,
		"searchindexer.exe":           true,
		"phoneexperiencehost.exe":     true,
		"gamebarpresencewriter.exe":   true,
		"windowsterminal.exe":         true,
		"conhost.exe":                 true,
		"csrss.exe":                   true,
		"wininit.exe":                 true,
		"winlogon.exe":                true,
		"svchost.exe":                 true,
		"lsass.exe":                   true,
		"services.exe":                true,
		"smss.exe":                    true,
		"sihost.exe":                  true,
		"fontdrvhost.exe":             true,
		"dllhost.exe":                 true,
		"taskmgr.exe":                 true,
		"cmd.exe":                     true,
		"powershell.exe":              true,
		"pwsh.exe":                    true,
		"msedgewebview2.exe":          true,
		"crashpad_handler.exe":        true,
		"audiodg.exe":                 true,
		"searchapp.exe":               true,
		"searchui.exe":                true,
		"mmc.exe":                     true,
		"perfmon.exe":                 true,
		"regedit.exe":                 true,
		"mstsc.exe":                   true,
		"snippingtool.exe":            true,
		"windowsinternal.composableshell.experiences.textinput.inputapp.exe": true,
	}
	if skip[exeLower] {
		return true
	}
	// Helper / background processes that show a hidden window
	suffixes := []string{
		"webhelper.exe",
		"helper.exe",
		"crashhandler.exe",
		"updater.exe",
		"update.exe",
		"crashpad.exe",
		"broker.exe",
		"host.exe",
	}
	for _, s := range suffixes {
		if strings.HasSuffix(exeLower, s) && exeLower != s {
			return true
		}
	}
	return false
}
