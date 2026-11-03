// Package vpn controls the sing-box core. The desktop implementation runs
// the bundled sing-box binary as a child process; the mobile path will
// later plug in sing-box's libbox via the same Manager interface.
package vpn

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// CoreBinary is the core shipped beside the running program, for a probe that
// starts its own copy next to the tunnel. The base directory holds user data
// only: on Windows the GUI and sing-box.exe both live in app\.
func CoreBinary() string {
	exe, err := os.Executable()
	if err != nil {
		return BinaryName
	}
	return filepath.Join(filepath.Dir(exe), BinaryName)
}

// LineLogger is the slice of the logger we need — kept tiny so this package
// doesn't import core/logging directly.
type LineLogger interface {
	Logf(format string, args ...interface{})
	PipeLines(tag string, r io.ReadCloser)
}

// Manager is the platform-agnostic VPN control surface. The same set of
// methods is implemented by process_windows.go (sing-box.exe) and stubbed
// out in process_other.go until the libbox-based mobile implementation
// lands.
type Manager interface {
	Start() error
	Stop() error
	Restart() error
}

// ClashAPIRunning is the source-of-truth liveness check shared by every
// implementation: ask the sing-box Clash API whether the core is answering.
// Returns true on HTTP 200 within 2s — the budget is deliberately loose,
// since a core that is busy pumping traffic can take a while to answer and
// a false "not running" is expensive (see health's downStreakThreshold).
//
// NOTE: this only tells you the *process* is alive — it does NOT tell you
// the tunnel is actually carrying traffic. Use the health package for that.
func ClashAPIRunning() bool {
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://127.0.0.1:9090/version")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}

var reCoreColour = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

// CoreReason is what the core said when it gave up: its FATAL line, or its last
// line, without colour, level, timer and the config path, which tells a user
// nothing.
func CoreReason(output, configPath string) string {
	last, fatal := "", ""
	for _, l := range strings.Split(reCoreColour.ReplaceAllString(output, ""), "\n") {
		if l = strings.TrimSpace(l); l == "" {
			continue
		}
		last = l
		if strings.Contains(l, "FATAL") {
			fatal = l
		}
	}
	line := last
	if fatal != "" {
		line = fatal[strings.Index(fatal, "FATAL")+len("FATAL"):]
		if strings.HasPrefix(line, "[") {
			if i := strings.Index(line, "]"); i >= 0 {
				line = line[i+1:]
			}
		}
		line = strings.TrimSpace(line)
	}
	line = strings.Replace(line, "decode config at "+configPath+": ", "", 1)
	line = strings.Replace(line, " at "+configPath, "", 1)
	if line == "" {
		return "ядро завершилось без объяснения"
	}
	return line
}
