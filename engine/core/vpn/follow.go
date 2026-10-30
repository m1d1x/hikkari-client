package vpn

import (
	"bufio"
	"io"
	"os"
	"time"
)

// followFile copies lines appended to path into log under tag, polling once
// a second, for as long as the process lives. It starts at the current end of
// the file: older lines are already in a previous engine log.
//
// Used where the core runs in a privileged process of its own (the macOS
// LaunchDaemon, the Windows service) and writes its output to a file the
// engine can read but did not open.
func followFile(path, tag string, log LineLogger) {
	pr, pw := io.Pipe()
	go log.PipeLines(tag, pr)
	go func() {
		var off int64
		if fi, err := os.Stat(path); err == nil {
			off = fi.Size()
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
		}
	}()
}
