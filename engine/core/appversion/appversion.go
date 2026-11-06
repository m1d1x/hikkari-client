// Package appversion is the release this process was started from.
//
// An installation carries its release in one file, VERSION, written by the
// build and read by hikkari-updater as "installed": <root>\app\VERSION on
// Windows, hikkari.app/Contents/Resources/VERSION on macOS. A running GUI
// reads it once, at start: the file may later name another release (an
// update swapped in, or a version on trial rolled back), while the code in
// memory stays the one that was started. The service reads the file each
// time and compares (svcproto.Request.AppVersion).
package appversion

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// FileFor is the VERSION file of the installation exe belongs to.
func FileFor(exe string, goos string) string {
	dir := filepath.Dir(exe)
	if goos == "darwin" {
		// hikkari.app/Contents/MacOS/<exe>
		return filepath.Join(filepath.Dir(dir), "Resources", "VERSION")
	}
	// <root>\app\<exe>
	return filepath.Join(dir, "VERSION")
}

// Read is the release named by the VERSION file at path; empty when there is
// none (a development build) or it cannot be read.
func Read(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	v := strings.TrimSpace(string(raw))
	if len(v) > 64 {
		return ""
	}
	return v
}

var (
	once sync.Once
	own  string
)

// Own is the release this process was started from, read at the first call.
// Capture it early (Core.Init) so a swap later in the process's life cannot
// change the answer. Empty for a development build.
func Own() string {
	once.Do(func() {
		exe, err := os.Executable()
		if err != nil {
			return
		}
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		own = Read(FileFor(exe, runtime.GOOS))
	})
	return own
}
