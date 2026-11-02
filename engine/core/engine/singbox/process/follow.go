package vpn

import (
	"bufio"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// maxCatchUp bounds how much of the file one start copies. A session can stay
// closed for days while a service log with diagnostics on keeps growing.
const maxCatchUp = 1 << 20

// followFile copies lines appended to path into log under tag, polling once
// a second, for as long as the process lives. It resumes where the previous
// engine stopped, kept in offsetPath: what the service wrote while no GUI was
// running, a crash trace from before a reboot above all, still reaches this log
// and through it support reports and the tester stream.
//
// Used where the core runs in a privileged process of its own (the macOS
// LaunchDaemon, the Windows service) and writes its output to a file the
// engine can read but did not open.
func followFile(path, offsetPath, tag string, log LineLogger) {
	pr, pw := io.Pipe()
	go log.PipeLines(tag, pr)
	go func() {
		var off int64
		if fi, err := os.Stat(path); err == nil {
			off = startOffset(offsetPath, fi.Size())
		}
		for {
			time.Sleep(time.Second)
			fi, err := os.Stat(path)
			if err != nil {
				continue
			}
			if fi.Size() < off {
				off = 0 // truncated or replaced
			}
			if fi.Size() == off {
				continue
			}
			f, err := os.Open(path)
			if err != nil {
				continue
			}
			if _, err := f.Seek(off, io.SeekStart); err == nil {
				r := bufio.NewReader(f)
				for {
					ln, err := r.ReadString('\n')
					if err != nil {
						break // partial last line: re-read next tick
					}
					off += int64(len(ln))
					_, _ = pw.Write([]byte(ln))
				}
			}
			f.Close()
			_ = os.WriteFile(offsetPath, []byte(strconv.FormatInt(off, 10)), 0o644)
		}
	}()
}

// startOffset is where a new engine starts reading a file of the given size.
// A saved position past the end means the file was replaced since.
func startOffset(offsetPath string, size int64) int64 {
	var off int64
	if raw, err := os.ReadFile(offsetPath); err == nil {
		if saved, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64); err == nil && saved >= 0 && saved <= size {
			off = saved
		}
	}
	if size-off > maxCatchUp {
		off = size - maxCatchUp
	}
	return off
}
