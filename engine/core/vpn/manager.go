// Package vpn controls the sing-box core. The desktop implementation runs
// the bundled sing-box binary as a child process; the mobile path will
// later plug in sing-box's libbox via the same Manager interface.
package vpn

import (
	"io"
	"net/http"
	"time"
)

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
// Returns true on HTTP 200 within 600ms.
//
// NOTE: this only tells you the *process* is alive — it does NOT tell you
// the tunnel is actually carrying traffic. Use the health package for that.
func ClashAPIRunning() bool {
	client := http.Client{Timeout: 600 * time.Millisecond}
	resp, err := client.Get("http://127.0.0.1:9090/version")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}
