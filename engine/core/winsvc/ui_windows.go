//go:build windows

package winsvc

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// The few dialogs the installer and uninstaller show. Elevated processes
// started through ShellExecute("runas") get a console that vanishes when
// they exit, so outcomes go to a message box; Quiet (CI, scripted installs)
// keeps them to stdout and the exit code.

// Quiet suppresses every dialog.
var Quiet bool

// RelaunchElevated starts this executable again with the same arguments
// through the UAC prompt. The caller exits right after.
func RelaunchElevated(args []string) error {
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

// Report shows the outcome and, on failure, exits with status 1.
func Report(ok bool, text string) {
	fmt.Println(text)
	if !Quiet {
		flags := uint32(windows.MB_OK | windows.MB_ICONINFORMATION)
		if !ok {
			flags = windows.MB_OK | windows.MB_ICONERROR
		}
		messageBox(text, flags)
	}
	if !ok {
		os.Exit(1)
	}
}

// Ask is a yes/no question; Quiet answers yes.
func Ask(text string) bool {
	if Quiet {
		return true
	}
	return messageBox(text, windows.MB_YESNO|windows.MB_ICONQUESTION) == idYes
}

// idYes is MessageBox's "Yes" (winuser.h IDYES); x/sys/windows has no name for it.
const idYes = 6

func messageBox(text string, flags uint32) int32 {
	t, _ := windows.UTF16PtrFromString(text)
	c, _ := windows.UTF16PtrFromString(productName)
	ret, _ := windows.MessageBox(0, t, c, flags|windows.MB_SETFOREGROUND)
	return ret
}
