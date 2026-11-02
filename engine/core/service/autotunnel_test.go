package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"NekkoVPN/core/autotunnel"
	"NekkoVPN/core/svcproto"
)

// newServerForTests builds a service around a core binary that does not exist:
// these tests never get as far as running one, they check what the service
// writes before that.
func newServerForTests(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := NewServer(Config{
		CoreDir:     dir,
		CoreBin:     filepath.Join(dir, "no-such-core"),
		CoreWorkDir: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, dir
}

// guiConfig is what the GUI engine compiles: an autotunnel rule set naming a
// file under the GUI's own base directory.
func guiConfig() map[string]interface{} {
	return map[string]interface{}{
		"inbounds": []interface{}{map[string]interface{}{"tag": "tun-in", "type": "tun"}},
		"route": map[string]interface{}{
			"rule_set": []interface{}{map[string]interface{}{
				"tag": autotunnel.Tag, "type": "local", "format": "source",
				"path": "/gui/base/auto-tunnel.json",
			}},
		},
		"experimental": map[string]interface{}{
			"cache_file": map[string]interface{}{"enabled": true, "path": "cache.db"},
		},
	}
}

func TestAutoTunnelOpWritesTheServiceOwnedRuleSet(t *testing.T) {
	s, dir := newServerForTests(t)
	// The GUI sends the list sorted (List.Hosts); the service keeps the order.
	resp := s.dispatch(svcproto.Request{Op: svcproto.OpAutoTunnel, Hosts: []string{"a.example", "b.example"}})
	if !resp.OK {
		t.Fatalf("autotunnel: %+v", resp)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "auto-tunnel.json"))
	if err != nil {
		t.Fatalf("the service did not write its rule set: %v", err)
	}
	var rs struct {
		Version int `json:"version"`
		Rules   []struct {
			Domain []string `json:"domain"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(raw, &rs); err != nil {
		t.Fatalf("rule set %s: %v", raw, err)
	}
	if rs.Version != 3 || len(rs.Rules) != 1 {
		t.Fatalf("rule set: %s", raw)
	}
	if !reflect.DeepEqual(rs.Rules[0].Domain, []string{"a.example", "b.example"}) {
		t.Fatalf("domains: %s", raw)
	}
}

func TestAutoTunnelOpRefusesWhatIsNotADomain(t *testing.T) {
	s, dir := newServerForTests(t)
	resp := s.dispatch(svcproto.Request{Op: svcproto.OpAutoTunnel, Hosts: []string{"not a domain"}})
	if resp.OK {
		t.Fatal("accepted a host that is not a domain")
	}
	if _, err := os.Stat(filepath.Join(dir, "auto-tunnel.json")); !os.IsNotExist(err) {
		t.Fatalf("a refused list left a file: %v", err)
	}
	if resp := s.dispatch(svcproto.Request{Op: svcproto.OpAutoTunnel, Hosts: []string{strings.Repeat("h", 300) + ".example"}}); resp.OK {
		t.Fatal("accepted a host longer than a domain may be")
	}
	// An empty list is a reset, not an error.
	if resp := s.dispatch(svcproto.Request{Op: svcproto.OpAutoTunnel}); !resp.OK {
		t.Fatalf("empty list refused: %+v", resp)
	}
}

func TestWriteConfigRewritesTheAutoTunnelRuleSet(t *testing.T) {
	s, dir := newServerForTests(t)
	raw, err := json.Marshal(guiConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.writeConfig(svcproto.EngineSingBox, raw); err != nil {
		t.Fatalf("the compiled config was refused: %v", err)
	}
	stored, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(stored, &cfg); err != nil {
		t.Fatal(err)
	}
	sets := cfg["route"].(map[string]interface{})["rule_set"].([]interface{})
	set := sets[0].(map[string]interface{})
	if set["path"] != filepath.Join(dir, "auto-tunnel.json") {
		t.Fatalf("rule set path = %v: the GUI's path was not rewritten", set["path"])
	}
	if set["format"] != "source" {
		t.Fatalf("format = %v", set["format"])
	}
	cache := cfg["experimental"].(map[string]interface{})["cache_file"].(map[string]interface{})
	if cache["path"] != filepath.Join(dir, "cache.db") {
		t.Fatalf("cache path = %v", cache["path"])
	}
}

func TestStartMakesSureTheRuleSetFileExists(t *testing.T) {
	s, dir := newServerForTests(t)
	raw, err := json.Marshal(guiConfig())
	if err != nil {
		t.Fatal(err)
	}
	// The core binary does not exist, so the start itself fails — after the
	// service has prepared everything the core needs to be able to start.
	resp := s.dispatch(svcproto.Request{Op: svcproto.OpStart, Config: raw})
	if resp.OK {
		t.Fatal("a core that does not exist started")
	}
	if _, err := os.Stat(filepath.Join(dir, "auto-tunnel.json")); err != nil {
		t.Fatalf("the core refuses to start over a missing rule set: %v", err)
	}
}
