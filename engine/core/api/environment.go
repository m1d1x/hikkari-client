package api

import (
	"os"
	"runtime"
	"runtime/debug"
)

// logEnvironment writes one block at init describing where and how the
// engine runs. A field report then starts with the facts we would otherwise
// have to ask for: OS and CPU, where the app was launched from, which build
// it is, and the state of the privileged half of the VPN.
func (c *Core) logEnvironment() {
	l := c.logger
	host, _ := os.Hostname()
	exe, _ := os.Executable()
	wd, _ := os.Getwd()
	l.Logf("[env] os=%s arch=%s go=%s cpus=%d host=%s uid=%d",
		runtime.GOOS, runtime.GOARCH, runtime.Version(), runtime.NumCPU(), host, os.Getuid())
	l.Logf("[env] exe=%s cwd=%s", exe, wd)
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision", "vcs.time", "vcs.modified":
				l.Logf("[env] build %s=%s", s.Key, s.Value)
			}
		}
	}
	go c.backend.Diagnose("init")
}
