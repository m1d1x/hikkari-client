package service

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"NekkoVPN/core/logging"
)

const (
	crashFile = "service-crash.log"
	// crashLogLimit bounds what one crash adds to the log: the panic and the
	// crashing goroutine come first, the other goroutines after them.
	crashLogLimit = 16 << 10
)

// captureCrashes keeps the trace of a crash of this process. Nobody reads a
// service's stderr, so without this a crash leaves only the restart behind and
// its cause is lost. The trace of the previous run moves to the log, from
// where it reaches support reports and the tester stream.
func captureCrashes(coreDir string, log *logging.Logger) {
	path := filepath.Join(coreDir, crashFile)
	if trace := readCrashTrace(path); trace != "" {
		log.Logf("service: the previous run crashed:\n%s", trace)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		log.Logf("service: crash capture unavailable: %v", err)
		return
	}
	defer f.Close()
	if err := debug.SetCrashOutput(f, debug.CrashOptions{}); err != nil {
		log.Logf("service: crash capture unavailable: %v", err)
	}
}

func readCrashTrace(path string) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if len(content) > crashLogLimit {
		content = content[:crashLogLimit]
	}
	return strings.TrimSpace(string(content))
}
