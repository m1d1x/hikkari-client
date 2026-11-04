package singbox

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"NekkoVPN/core/engine"
	configfile "NekkoVPN/core/engine/singbox/config"
	"NekkoVPN/core/profile"
	"NekkoVPN/core/routing"
)

// A WireGuard endpoint resolves a name itself, and the core's own lookups (the
// delay test, the endpoint's dial for it) carry no inbound. The DNS block used
// to answer every such lookup with an empty NOERROR, so each probe of an
// Amnezia profile failed with 503 (tester-002, 04.11). Checked is only that the
// lookup is not answered empty: offline it fails in the resolver instead, and
// the peer is unreachable, so the probe itself always times out.
func TestEndpointLookupWithoutInboundResolves(t *testing.T) {
	core := realCore(t)
	dir := t.TempDir()
	b := &Backend{config: configfile.New(filepath.Join(dir, "config.json"))}
	decoded, err := b.Decode(pskAwgConf)
	if err != nil {
		t.Fatal(err)
	}
	decoded[0].Name = "amnezia"
	if err := b.Configure(engine.Configuration{Profiles: []profile.Profile{decoded[0]}, ActiveProfile: "amnezia", Routing: routing.DefaultRoutingState()}); err != nil {
		t.Fatal(err)
	}
	cfg, err := b.config.Read()
	if err != nil {
		t.Fatal(err)
	}
	// The TUN needs privileges; rule sets name files only a real install has.
	port := freePort(t)
	cfg["inbounds"] = []interface{}{}
	cfg["log"] = map[string]interface{}{"level": "debug", "output": filepath.Join(dir, "core.log")}
	cfg["experimental"] = map[string]interface{}{"clash_api": map[string]interface{}{"external_controller": fmt.Sprintf("127.0.0.1:%d", port)}}
	route := cfg["route"].(map[string]interface{})
	delete(route, "rule_set")
	route["rules"] = withoutRuleSets(route["rules"])
	dns := cfg["dns"].(map[string]interface{})
	dns["rules"] = withoutRuleSets(dns["rules"])
	raw, _ := json.Marshal(cfg)
	path := filepath.Join(dir, "run.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(core, "run", "-c", path)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	api := fmt.Sprintf("http://127.0.0.1:%d", port)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if resp, err := http.Get(api + "/version"); err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("core API did not come up")
		}
	}
	resp, err := http.Get(api + "/proxies/p-amnezia/delay?timeout=1000&url=http://cp.cloudflare.com/generate_204")
	if err == nil {
		resp.Body.Close()
	}
	logText, _ := os.ReadFile(filepath.Join(dir, "core.log"))
	if !strings.Contains(string(logText), "dns: lookup domain") || strings.Contains(string(logText), ": empty result") {
		t.Fatalf("the endpoint found no address for its own lookup:\n%s", logText)
	}
}

func withoutRuleSets(raw interface{}) []interface{} {
	rules, _ := raw.([]interface{})
	out := make([]interface{}, 0, len(rules))
	for _, r := range rules {
		if m, ok := r.(map[string]interface{}); ok && m["rule_set"] != nil {
			continue
		}
		out = append(out, r)
	}
	return out
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}
