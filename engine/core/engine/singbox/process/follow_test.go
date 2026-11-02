package vpn

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"NekkoVPN/core/logging"
)

func TestStartOffset(t *testing.T) {
	dir := t.TempDir()
	saved := filepath.Join(dir, "offset")
	write := func(v string) {
		if err := os.WriteFile(saved, []byte(v), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := startOffset(saved, 100); got != 0 {
		t.Errorf("no saved position: %d, want the start of the file", got)
	}
	if got := startOffset(saved, maxCatchUp+100); got != 100 {
		t.Errorf("no saved position, big file: %d, want the last %d bytes", got, maxCatchUp)
	}
	write("40")
	if got := startOffset(saved, 100); got != 40 {
		t.Errorf("saved position: %d", got)
	}
	if got := startOffset(saved, 10); got != 0 {
		t.Errorf("file replaced since: %d", got)
	}
	write("garbage")
	if got := startOffset(saved, 100); got != 0 {
		t.Errorf("damaged position: %d", got)
	}
}

type collectLogger struct {
	mu    sync.Mutex
	lines []string
}

func (c *collectLogger) Logf(format string, args ...interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = append(c.lines, fmt.Sprintf(format, args...))
}

func (c *collectLogger) PipeLines(tag string, r io.ReadCloser) {
	// The real collapsing logic, so a trace is proven to survive it.
	path := filepath.Join(os.TempDir(), fmt.Sprintf("follow-test-%d.log", time.Now().UnixNano()))
	defer os.Remove(path)
	l := logging.New(path)
	defer l.Close()
	l.SetTap(func(line string) { c.Logf("%s", line) })
	l.PipeLines(tag, r)
}

func (c *collectLogger) text() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.lines, "\n")
}

// A crash before the GUI started must still reach the engine log, whole, while
// what the previous engine already copied must not come again.
func TestFollowFileCatchesUpWithACrashTraceFromBeforeTheGUI(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "service.log")
	copied := "already copied by the previous engine\n"
	trace := "service: the previous run crashed:\n" +
		"panic: runtime error: invalid memory address\n\n" +
		"goroutine 41 [running]:\n" +
		"NekkoVPN/core/winsvc.describeService(0x1, 0x2)\n" +
		"\tcore/winsvc/dpi_windows.go:100 +0x1a\n" +
		"NekkoVPN/core/winsvc.(*dpiSystem).Services(0x3)\n" +
		"\tcore/winsvc/dpi_windows.go:71 +0x2b\n"
	if err := os.WriteFile(path, []byte(copied+trace), 0o644); err != nil {
		t.Fatal(err)
	}
	offset := filepath.Join(dir, "offset")
	if err := os.WriteFile(offset, []byte(fmt.Sprint(len(copied))), 0o644); err != nil {
		t.Fatal(err)
	}

	log := &collectLogger{}
	followFile(path, offset, "service", log)

	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(log.text(), "dpi_windows.go:71") {
		if time.Now().After(deadline) {
			t.Fatalf("trace did not arrive: %q", log.text())
		}
		time.Sleep(50 * time.Millisecond)
	}
	got := log.text()
	for _, want := range []string{"panic: runtime error", "goroutine 41", "describeService", "dpi_windows.go:100", "(*dpiSystem).Services"} {
		if !strings.Contains(got, want) {
			t.Errorf("trace lost %q: %q", want, got)
		}
	}
	if strings.Contains(got, "already copied") {
		t.Errorf("copied again: %q", got)
	}
	// The position is saved after the copy; give it the same tick.
	for time.Now().Before(deadline) {
		if raw, _ := os.ReadFile(offset); string(raw) == fmt.Sprint(len(copied)+len(trace)) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("position not saved")
}
