//go:build darwin

package vpn

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Diagnose dumps everything that decides whether the core can come up on
// this Mac into the engine log, tagged "[mac:<reason>]". It only reads.
//
// The privileged half (launchd job, root-owned files, the core's own output)
// is invisible from the app's normal log, and a failed start otherwise shows
// up as a single "sing-box не поднялся" line. This is what we used to ask
// testers for one Terminal command at a time.
func (m *ProcessManager) Diagnose(reason string) {
	tag := "mac:" + reason
	logf := func(format string, args ...interface{}) {
		m.log.Logf("["+tag+"] "+format, args...)
	}
	run := func(name string, args ...string) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
		head := strings.Join(append([]string{name}, args...), " ")
		if err != nil {
			logf("$ %s → %v", head, err)
		} else {
			logf("$ %s", head)
		}
		for _, ln := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
			if ln != "" {
				logf("  %s", ln)
			}
		}
	}

	logf("---- begin ----")
	run("/usr/bin/sw_vers")
	run("/usr/bin/uname", "-m")
	// 1 = this process runs under Rosetta, i.e. an Intel slice on Apple Silicon.
	run("/usr/sbin/sysctl", "-n", "sysctl.proc_translated", "hw.optional.arm64", "machdep.cpu.brand_string")

	if exe, err := os.Executable(); err == nil {
		bundle := exe
		if i := strings.Index(exe, ".app/"); i >= 0 {
			bundle = exe[:i+len(".app")]
		}
		logf("app bundle: %s", bundle)
		run("/usr/bin/xattr", "-l", bundle)
		run("/usr/bin/codesign", "-dv", bundle)
		run("/usr/sbin/spctl", "--assess", "-vv", bundle)
	}

	bin, err := m.resolveBinary()
	if err != nil {
		logf("core binary: %v", err)
	} else {
		logf("core binary (source): %s", bin)
		m.describeBinary(bin, run)
	}
	logf("config: %s", m.configPath)
	if bin != "" {
		run(bin, "check", "-c", m.configPath)
	}

	plist, supervisor := m.plist(), m.supervisor(installedBin)
	logf("installed: plist=%v supervisor=%v core-same=%v",
		fileHas(daemonPlist, plist), fileHas(supervisorSh, supervisor),
		bin != "" && sameFile(bin, installedBin))
	run("/bin/ls", "-la", daemonDir, daemonPlist)
	if _, err := os.Stat(installedBin); err == nil {
		logf("core binary (installed): %s", installedBin)
		m.describeBinary(installedBin, run)
	}
	run("/bin/launchctl", "print", "system/"+daemonLabel)

	_, sentinelErr := os.Stat(m.runFile())
	logf("sentinel %s exists=%v, clash api up=%v", m.runFile(), sentinelErr == nil, ClashAPIRunning())
	run("/bin/sh", "-c", "ps -axo pid,ppid,user,etime,comm | grep -E 'sing-box|singbox-supervisor' | grep -v grep")
	run("/bin/sh", "-c", "ifconfig | grep -A3 '^utun'")

	logf("last lines of %s:", m.logFile())
	for _, ln := range tailLines(m.logFile(), 80) {
		logf("  | %s", ln)
	}
	logf("---- end ----")
}

func (m *ProcessManager) describeBinary(path string, run func(string, ...string)) {
	run("/usr/bin/file", path)
	run("/usr/bin/xattr", "-l", path)
	run("/usr/bin/codesign", "-dv", path)
	// Actually executing it is the only check that catches "bad CPU type",
	// a Gatekeeper kill or a missing dylib the same way launchd would.
	run(path, "version")
}

func tailLines(path string, n int) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return []string{fmt.Sprintf("<%v>", err)}
	}
	const max = 64 * 1024
	if len(data) > max {
		data = data[len(data)-max:]
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

var followOnce sync.Once

// followCoreLog copies new lines of singbox.log (the supervisor's and the
// core's stdout/stderr, written by launchd as root) into the engine log under
// the "singbox" tag, so the one file a tester sends has both halves. Starts
// at the current end of the file: older lines are already in a previous
// engine log or in Diagnose's tail.
func (m *ProcessManager) followCoreLog() {
	followOnce.Do(func() {
		pr, pw := io.Pipe()
		go m.log.PipeLines("singbox", pr)
		go func() {
			path := m.logFile()
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
	})
}
