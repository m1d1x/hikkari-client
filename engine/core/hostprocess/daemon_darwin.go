//go:build darwin

// Package hostprocess provides OS process hosting without engine configuration
// knowledge. Adapters supply the binary, arguments and readiness probe.
package hostprocess

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Daemon struct {
	mu                      sync.Mutex
	Name, Binary, Directory string
	DNSAddress              string
	Args                    []string
	Ready                   func() bool
}

func (d *Daemon) paths() (label, root, run, log string) {
	sum := sha256.Sum256([]byte(d.Directory))
	label = fmt.Sprintf("com.hikkari.%s.%x", d.Name, sum[:6])
	root = filepath.Join("/Library/Application Support/hikkari", label)
	return label, root, filepath.Join(d.Directory, d.Name+".run"), filepath.Join(d.Directory, d.Name+".log")
}
func (d *Daemon) Start(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	label, root, run, log := d.paths()
	if _, err := os.Stat(filepath.Join(root, "child.pid")); err == nil && !d.IsRunning() {
		if err := os.Remove(run); err != nil && !os.IsNotExist(err) {
			return err
		}
		if !wait(ctx, 15*time.Second, func() bool { _, err := os.Stat(filepath.Join(root, "child.pid")); return os.IsNotExist(err) }) {
			return fmt.Errorf("%s: предыдущий процесс не остановился", d.Name)
		}
	}
	bin := filepath.Join(root, filepath.Base(d.Binary))
	supervisor := filepath.Join(root, "supervisor.sh")
	plistPath := "/Library/LaunchDaemons/" + label + ".plist"
	script := d.supervisor(bin, run, log)
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>Label</key><string>%s</string><key>ProgramArguments</key><array><string>/bin/sh</string><string>%s</string></array><key>RunAtLoad</key><true/><key>KeepAlive</key><true/><key>StandardOutPath</key><string>%s</string><key>StandardErrorPath</key><string>%s</string></dict></plist>`, xml(label), xml(supervisor), xml(log), xml(log))
	src, err := os.ReadFile(d.Binary)
	if err != nil {
		return err
	}
	installed, _ := os.ReadFile(bin)
	needInstall := sha256.Sum256(src) != sha256.Sum256(installed) || !matches(supervisor, script) || !matches(plistPath, plist)
	// A loaded job can be idle after a restart; its presence is checked separately
	// from file hashes so a reboot never leaves an apparently installed dead host.
	if !needInstall {
		needInstall = exec.CommandContext(ctx, "launchctl", "print", "system/"+label).Run() != nil
	}
	if needInstall {
		if err := d.install(ctx, label, root, bin, supervisor, plistPath, script, plist); err != nil {
			return err
		}
	}
	if err := os.WriteFile(run, []byte("up\n"), 0600); err != nil {
		return err
	}
	if wait(ctx, 20*time.Second, d.IsRunning) {
		return nil
	}
	_ = os.Remove(run)
	if reason, err := os.ReadFile(filepath.Join(root, "last-error")); err == nil {
		return fmt.Errorf("%s: %s", d.Name, strings.TrimSpace(string(reason)))
	}
	return fmt.Errorf("%s не запустился; журнал: %s", d.Name, log)
}
func (d *Daemon) Stop() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, root, run, _ := d.paths()
	if err := os.Remove(run); err != nil && !os.IsNotExist(err) {
		return err
	}
	if wait(context.Background(), 15*time.Second, func() bool {
		_, pidErr := os.Stat(filepath.Join(root, "child.pid"))
		_, dnsErr := os.Stat(filepath.Join(root, "dns-state"))
		return os.IsNotExist(pidErr) && os.IsNotExist(dnsErr) && !d.IsRunning()
	}) {
		return nil
	}
	return fmt.Errorf("%s не остановился", d.Name)
}
func (d *Daemon) IsRunning() bool {
	_, root, _, _ := d.paths()
	if _, err := os.Stat(filepath.Join(root, "child.pid")); err != nil {
		return false
	}
	return d.Ready != nil && d.Ready()
}
func (d *Daemon) supervisor(bin, run, log string) string {
	args := []string{quote(bin)}
	for _, arg := range d.Args {
		args = append(args, quote(arg))
	}
	_, root, _, _ := d.paths()
	replacements := strings.NewReplacer("@RUN@", quote(run), "@WORK@", quote(d.Directory), "@DNS@", quote(d.DNSAddress), "@STATE@", quote(filepath.Join(root, "dns-state")), "@PID@", quote(filepath.Join(root, "child.pid")), "@ERROR@", quote(filepath.Join(root, "last-error")), "@COMMAND@", strings.Join(args, " "), "@LOG@", quote(log))
	return replacements.Replace(daemonScript)
}

// DNS ownership belongs to the platform host, not to any transport engine.
// Backups are root-owned and restored on disconnect, including partial setup.
const daemonScript = `#!/bin/sh
RUN=@RUN@
WORK=@WORK@
DNS=@DNS@
STATE=@STATE@
PIDFILE=@PID@
ERROR=@ERROR@
DNS_TOOL=/usr/sbin/networksetup
CHILD=""
umask 022

restore_dns() {
  [ -d "$STATE" ] || return 0
  failed=0
  for record in "$STATE"/backup-*; do
    [ -f "$record" ] || continue
    service=$(sed -n '1p' "$record")
    servers=$(sed '1d' "$record")
    if [ -n "$servers" ]; then
      set -- $servers
    else
      set -- Empty
    fi
    if "$DNS_TOOL" -setdnsservers "$service" "$@"; then
      rm -f "$record"
    else
      failed=1
      echo "Failed to restore DNS for $service" > "$ERROR"
    fi
  done
  if [ "$failed" -eq 0 ]; then
    rm -f "$STATE/ready" "$STATE/services" "$STATE/services.raw"
    rmdir "$STATE" 2>/dev/null
  fi
  return "$failed"
}

apply_dns() {
  [ -n "$DNS" ] || return 0
  [ ! -f "$STATE/ready" ] || return 0
  mkdir -p "$STATE" || return 1
  "$DNS_TOOL" -listallnetworkservices > "$STATE/services.raw" || return 1
  sed '1d' "$STATE/services.raw" > "$STATE/services"
  rm -f "$STATE/services.raw"
  index=0
  while IFS= read -r service; do
    case "$service" in ""|\**) continue ;; esac
    index=$((index+1))
    record="$STATE/backup-$index"
    if [ ! -f "$record" ]; then
      servers=$("$DNS_TOOL" -getdnsservers "$service") || return 1
      printf '%s\n' "$service" > "$record"
      printf '%s\n' "$servers" | LC_ALL=C grep -E '^[0-9a-fA-F:.%]+$' >> "$record" || true
    fi
    "$DNS_TOOL" -setdnsservers "$service" "$DNS" || return 1
  done < "$STATE/services"
  touch "$STATE/ready"
}

stop_child() {
  [ -n "$CHILD" ] || return 0
  kill -TERM "$CHILD" 2>/dev/null
  n=0
  while kill -0 "$CHILD" 2>/dev/null && [ "$n" -lt 30 ]; do sleep 0.1; n=$((n+1)); done
  kill -KILL "$CHILD" 2>/dev/null
  wait "$CHILD" 2>/dev/null
  CHILD=""
  rm -f "$PIDFILE"
}
cleanup() { stop_child; restore_dns; }
trap 'cleanup; exit 0' TERM INT
cd "$WORK" || exit 1
while :; do
  if [ -f "$RUN" ]; then
    if [ -z "$CHILD" ] || ! kill -0 "$CHILD" 2>/dev/null; then
      if apply_dns; then
        rm -f "$ERROR"
        @COMMAND@ >>@LOG@ 2>&1 &
        CHILD=$!
        echo "$CHILD" > "$PIDFILE"
      else
        echo "Failed to configure system DNS" > "$ERROR"
        restore_dns
        rm -f "$RUN"
      fi
    fi
  else
    cleanup
  fi
  sleep 0.2
done
`

func (d *Daemon) install(ctx context.Context, label, root, bin, supervisor, plistPath, script, plist string) error {
	stage, err := os.MkdirTemp(d.Directory, ".host-install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	sp, pp, ip := filepath.Join(stage, "supervisor.sh"), filepath.Join(stage, "daemon.plist"), filepath.Join(stage, "install.sh")
	if err := os.WriteFile(sp, []byte(script), 0600); err != nil {
		return err
	}
	if err := os.WriteFile(pp, []byte(plist), 0600); err != nil {
		return err
	}
	install := fmt.Sprintf(`#!/bin/sh
set -e
launchctl bootout %s 2>/dev/null || true
n=0
while launchctl print %s >/dev/null 2>&1 && [ "$n" -lt 50 ]; do sleep 0.2; n=$((n+1)); done
install -d -o root -g wheel -m 755 %s
install -o root -g wheel -m 755 %s %s
install -o root -g wheel -m 755 %s %s
install -o root -g wheel -m 644 %s %s
launchctl enable %s
launchctl bootstrap system %s
`, quote("system/"+label), quote("system/"+label), quote(root), quote(d.Binary), quote(bin), quote(sp), quote(supervisor), quote(pp), quote(plistPath), quote("system/"+label), quote(plistPath))

	if err := os.WriteFile(ip, []byte(install), 0700); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "osascript", "-e", "on run argv", "-e", `do shell script "/bin/sh " & quoted form of (item 1 of argv) with prompt "Hikkari устанавливает процесс VPN-ядра." with administrator privileges`, "-e", "end run", ip)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("установка процесса %s: %s", d.Name, strings.TrimSpace(string(out)))
	}
	return nil
}
func matches(path, want string) bool {
	b, err := os.ReadFile(path)
	return err == nil && string(b) == want
}
func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
func xml(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;").Replace(s)
}
func wait(ctx context.Context, duration time.Duration, probe func() bool) bool {
	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
		if probe() {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(100 * time.Millisecond):
		}
	}
	return false
}
