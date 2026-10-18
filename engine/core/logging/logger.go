// Package logging is a tiny file logger with size-based rotation. It also
// provides PipeLines, used to fan stdout/stderr of the sing-box child into
// the same file with a tag prefix.
package logging

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
	"strings"
	"sync"
)

// MaxBytes is the rotation threshold. When the active log grows past this,
// it gets renamed to <path>.old and a fresh file starts.
const MaxBytes = 512 * 1024

// TailBytes is the size of the tail returned by Tail() for in-app debugging.
const TailBytes = 16 * 1024

// Logger is a goroutine-safe rotating file logger.
type Logger struct {
	path string

	mu    sync.Mutex
	file  *os.File
	std   *log.Logger
	bytes int64
}

// New opens (or creates) the log file. If the file is already past MaxBytes
// it's rotated up front. On open failure the logger falls back to stderr
// silently — logging must never block startup.
func New(path string) *Logger {
	l := &Logger{path: path}
	if fi, err := os.Stat(path); err == nil && fi.Size() > MaxBytes {
		_ = os.Rename(path, path+".old")
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		l.std = log.New(os.Stderr, "", log.LstdFlags|log.Lmicroseconds)
		return l
	}
	l.file = f
	if fi, err := f.Stat(); err == nil {
		l.bytes = fi.Size()
	}
	l.std = log.New(f, "", log.LstdFlags|log.Lmicroseconds)
	return l
}

// Path returns the active log file path.
func (l *Logger) Path() string { return l.path }

// Logf writes a formatted line and rotates if we crossed MaxBytes.
func (l *Logger) Logf(format string, args ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.std == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	l.std.Println(msg)
	l.bytes += int64(len(msg)) + 32
	if l.file != nil && l.bytes > MaxBytes {
		_ = l.file.Close()
		_ = os.Rename(l.path, l.path+".old")
		f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err == nil {
			l.file = f
			l.std = log.New(f, "", log.LstdFlags|log.Lmicroseconds)
			l.bytes = 0
		}
	}
}

// Tail returns the last ~TailBytes of the log file as a string.
func (l *Logger) Tail() string {
	data, err := os.ReadFile(l.path)
	if err != nil {
		return ""
	}
	if len(data) > TailBytes {
		data = data[len(data)-TailBytes:]
	}
	return string(data)
}

var (
	reConnID    = regexp.MustCompile(`\[\d+\s+\d+ms\]`)
	reTimestamp = regexp.MustCompile(`^\+\d{4}\s+\d{4}-\d{2}-\d{2}\s+\d{2}:\d{2}:\d{2}(\.\d+)?\s+`)
)

func normalizeLogLine(s string) string {
	s = reTimestamp.ReplaceAllString(s, "")
	s = reConnID.ReplaceAllString(s, "[id]")
	return s
}

// PipeLines reads r line-by-line and logs each non-empty line with the given
// tag prefix. Consecutive identical/repetitive messages are collapsed so the
// log does not get flooded by thousands of duplicate connection messages.
func (l *Logger) PipeLines(tag string, r io.ReadCloser) {
	defer r.Close()
	scanner := bufio.NewScanner(r)
	var lastCore string
	var count int

	flush := func() {
		if count > 1 {
			l.Logf("[%s] ... previous message repeated %d times", tag, count-1)
		}
		count = 0
	}

	for scanner.Scan() {
		ln := strings.TrimSpace(scanner.Text())
		if ln == "" {
			continue
		}
		core := normalizeLogLine(ln)
		if core == lastCore && core != "" {
			count++
			if count >= 100 {
				flush()
				lastCore = core
				count = 1
			}
			continue
		}
		flush()
		lastCore = core
		count = 1
		l.Logf("[%s] %s", tag, ln)
	}
	flush()
}

// Close releases the underlying file. Safe to call multiple times.
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}
