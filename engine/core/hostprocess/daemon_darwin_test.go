//go:build darwin

package hostprocess

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func waitFor(t *testing.T, f func() bool) {
	t.Helper()
	for end := time.Now().Add(6 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if f() {
			return
		}
	}
	t.Fatal("supervisor did not reach expected state")
}
func exists(path string) bool { _, err := os.Stat(path); return err == nil }
func TestSupervisorRestoresDNSWithoutSystemChanges(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "disconnect", true: "setup-rollback"}[fail], func(t *testing.T) {
			dir := t.TempDir()
			root := filepath.Join(dir, "Root With Spaces")
			if err := os.Mkdir(root, 0700); err != nil {
				t.Fatal(err)
			}
			tool := filepath.Join(dir, "fake-networksetup")
			fake := `#!/bin/sh
case "$1" in
-listallnetworkservices) printf 'An asterisk denotes disabled services.\nWi-Fi\nUSB LAN\n*Disabled\n' ;;
-getdnsservers)
  case "$2" in "Wi-Fi") printf '9.9.9.9\nfd00::1\n' ;; "USB LAN") echo "There aren't any DNS Servers set on USB LAN." ;; *) exit 2 ;; esac ;;
-setdnsservers)
  printf '%s|' "$@" >> "$FAKE_DNS_LOG"
  printf '\n' >> "$FAKE_DNS_LOG"
  if [ "$FAKE_DNS_FAIL" = 1 ] && [ "$3" = '172.31.7.1' ]; then exit 1; fi ;;
*) exit 2 ;;
esac
`
			if err := os.WriteFile(tool, []byte(fake), 0700); err != nil {
				t.Fatal(err)
			}
			child := filepath.Join(dir, "fake-engine")
			if err := os.WriteFile(child, []byte("#!/bin/sh\ntrap 'exit 0' TERM INT\nwhile :; do sleep 0.1; done\n"), 0700); err != nil {
				t.Fatal(err)
			}
			d := &Daemon{Name: "independent", Directory: dir, Binary: child, DNSAddress: "172.31.7.1", Args: []string{"an argument with spaces"}}
			_, originalRoot, run, log := d.paths()
			script := strings.ReplaceAll(d.supervisor(child, run, log), originalRoot, root)
			script = strings.Replace(script, "DNS_TOOL=/usr/sbin/networksetup", "DNS_TOOL="+quote(tool), 1)
			path := filepath.Join(dir, "supervisor.sh")
			if err := os.WriteFile(path, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command("/bin/sh", "-n", path).CombinedOutput(); err != nil {
				t.Fatalf("%v %s", err, out)
			}
			if err := os.WriteFile(run, []byte("up"), 0600); err != nil {
				t.Fatal(err)
			}
			dnsLog := filepath.Join(dir, "dns.log")
			cmd := exec.Command("/bin/sh", path)
			cmd.Env = append(os.Environ(), "FAKE_DNS_LOG="+dnsLog)
			if fail {
				cmd.Env = append(cmd.Env, "FAKE_DNS_FAIL=1")
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				_ = cmd.Process.Signal(os.Interrupt)
				done := make(chan struct{})
				go func() { _ = cmd.Wait(); close(done) }()
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					_ = cmd.Process.Kill()
					<-done
				}
			}()
			if fail {
				waitFor(t, func() bool { return !exists(run) && !exists(filepath.Join(root, "dns-state")) })
			} else {
				waitFor(t, func() bool { return exists(filepath.Join(root, "child.pid")) })
				if err := os.Remove(run); err != nil {
					t.Fatal(err)
				}
				waitFor(t, func() bool {
					return !exists(filepath.Join(root, "child.pid")) && !exists(filepath.Join(root, "dns-state"))
				})
			}
			data, err := os.ReadFile(dnsLog)
			if err != nil {
				t.Fatal(err)
			}
			calls := string(data)
			if !strings.Contains(calls, "-setdnsservers|Wi-Fi|9.9.9.9|fd00::1|") {
				t.Fatalf("explicit DNS not restored: %s", calls)
			}
			if !fail && !strings.Contains(calls, "-setdnsservers|USB LAN|Empty|") {
				t.Fatalf("DHCP DNS not restored: %s", calls)
			}
			if strings.Contains(calls, "Disabled") {
				t.Fatal("disabled service was modified")
			}
		})
	}
}
