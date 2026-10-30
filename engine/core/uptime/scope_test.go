package uptime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLegacyServiceHistoryUsesPortableProfileID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "services.json")
	if err := os.WriteFile(path, []byte(`{"p-server|YouTube":[{"ts":1,"ok":true,"rtt_ms":42}],"p-p-server|YouTube":[{"ts":2,"ok":true,"rtt_ms":84}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	mapping := map[string]string{"p-server": "server", "p-p-server": "p-server"}
	tracker := New(path)
	tracker.Load()
	tracker.MigrateScopes(mapping)
	tracker.Save()
	reloaded := New(path)
	reloaded.Load()
	reloaded.MigrateScopes(mapping)
	history := reloaded.Snapshot()
	if len(history["server|YouTube"]) != 1 || history["server|YouTube"][0].RttMs != 42 {
		t.Fatal("first profile history changed")
	}
	if len(history["p-server|YouTube"]) != 1 || history["p-server|YouTube"][0].RttMs != 84 {
		t.Fatal("profile IDs overlap native tags after restart")
	}
	if len(history) != 2 {
		t.Fatal("unexpected history scope after migration")
	}
}
