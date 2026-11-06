//go:build darwin

package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"NekkoVPN/core/svcproto"
)

// reloadServer runs a fake core that speaks the control protocol of core patch
// 0009: it answers every reload command, applied unless the file `decline`
// exists in its directory, and records the commands it got.
func reloadServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	core := filepath.Join(dir, "core")
	script := `#!/bin/sh
if [ "$1" = "run" ] && [ "$2" = "--help" ]; then echo "      --hikkari-control   read hot reload commands"; exit 0; fi
while read cmd hash; do
  echo "$cmd $hash" >> "` + dir + `/commands"
  if [ -f "` + dir + `/decline" ]; then
    echo "hikkari:reload-result {\"hash\":\"$hash\",\"applied\":false,\"declined\":\"changed outside outbounds: inbounds\"}"
  else
    echo "noise line from the core log"
    echo "hikkari:reload-result {\"hash\":\"$hash\",\"applied\":true,\"replaced\":[\"p-1\"],\"groups\":[\"proxy\"]}"
  fi
done
exec sleep 60
`
	if err := os.WriteFile(core, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(Config{CoreDir: dir, CoreBin: core, CoreWorkDir: dir, CoreReady: fakeCoreUp})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.stopCores() })
	return s, dir
}

func reloadRequest(level, hash string) svcproto.Request {
	return svcproto.Request{Op: svcproto.OpReload, Config: json.RawMessage(`{"inbounds":[],"log":{"level":"` + level + `"}}`), ConfigHash: hash}
}

func TestReloadAppliesInPlace(t *testing.T) {
	s, dir := reloadServer(t)
	startCoreForTest(t, s, "first")
	pid := s.dispatch(svcproto.Request{Op: svcproto.OpStatus}).Pid
	resp := s.dispatch(reloadRequest("second", "second"))
	if !resp.OK || resp.Reload == nil || !resp.Reload.Applied {
		t.Fatalf("reload: %+v %+v", resp, resp.Reload)
	}
	if resp.Pid != pid {
		t.Fatalf("the core was restarted: pid %d -> %d", pid, resp.Pid)
	}
	if resp.ConfigHash != "second" {
		t.Fatalf("stored hash %q after an applied reload", resp.ConfigHash)
	}
	stored, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	if !strings.Contains(string(stored), "second") {
		t.Fatalf("stored config is not the reloaded one: %s", stored)
	}
	commands, _ := os.ReadFile(filepath.Join(dir, "commands"))
	if !strings.HasPrefix(string(commands), "hikkari:reload ") {
		t.Fatalf("core got %q", commands)
	}
}

func TestReloadDeclinedKeepsTheStoredConfig(t *testing.T) {
	s, dir := reloadServer(t)
	startCoreForTest(t, s, "first")
	before, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	if err := os.WriteFile(filepath.Join(dir, "decline"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	resp := s.dispatch(reloadRequest("second", "second"))
	if !resp.OK || resp.Reload == nil || resp.Reload.Applied || resp.Reload.Declined == "" {
		t.Fatalf("reload: %+v %+v", resp, resp.Reload)
	}
	if resp.ConfigHash != "first" {
		t.Fatalf("a declined reload left hash %q", resp.ConfigHash)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	if string(after) != string(before) {
		t.Fatalf("a declined reload changed the stored config")
	}
}

func TestReloadWithoutRunningCoreIsDeclined(t *testing.T) {
	s, _ := reloadServer(t)
	resp := s.dispatch(reloadRequest("second", "second"))
	if !resp.OK || resp.Reload == nil || resp.Reload.Applied || resp.Reload.Declined != "core is not running" {
		t.Fatalf("reload: %+v %+v", resp, resp.Reload)
	}
}

func TestReloadOfACoreWithoutControlIsDeclined(t *testing.T) {
	s := updateServer(t, Config{}) // its core has no --hikkari-control
	startCoreForTest(t, s, "first")
	resp := s.dispatch(reloadRequest("second", "second"))
	if !resp.OK || resp.Reload == nil || resp.Reload.Applied || resp.Reload.Declined != "core without hot reload" {
		t.Fatalf("reload: %+v %+v", resp, resp.Reload)
	}
	if resp.ConfigHash != "first" {
		t.Fatalf("hash %q", resp.ConfigHash)
	}
}
