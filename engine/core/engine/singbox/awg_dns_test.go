package singbox

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"NekkoVPN/core/engine"
	configfile "NekkoVPN/core/engine/singbox/config"
	"NekkoVPN/core/profile"
	"NekkoVPN/core/routing"
)

const testVLESS = "vless://11111111-2222-3333-4444-555555555555@re.example.com:443?security=reality&sni=www.microsoft.com&fp=firefox&pbk=lTmjpHaGYBYQ8T5oogAWheEzhjCnzFwvoaZYQevHMgQ&sid=abcd&flow=xtls-rprx-vision#RE"

func awgConfWithDNS(dns string) string {
	return strings.Replace(pskAwgConf, "Address = 10.8.1.7/32\n", "Address = 10.8.1.7/32\nDNS = "+dns+"\n", 1)
}

// The [Interface] DNS line is the server the config's owner wants the names
// resolved by. Import keeps its addresses (search domains are not resolvers),
// export gives them back.
func TestAwgDNSSurvivesImportAndExport(t *testing.T) {
	first, err := ParseURI(awgConfWithDNS("172.29.172.254, 1.0.0.1, corp.example"))
	if err != nil {
		t.Fatal(err)
	}
	want := []interface{}{"172.29.172.254", "1.0.0.1"}
	if !reflect.DeepEqual(first.Config["dns"], want) {
		t.Fatalf("imported dns %v, want %v", first.Config["dns"], want)
	}
	link, err := ExportURI(profile.Profile{Kind: first.Kind, Config: first.Config})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(link, "\nDNS = 172.29.172.254, 1.0.0.1\n") {
		t.Fatalf("exported config lost the DNS line:\n%s", link)
	}
	again, err := ParseURI(link)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again.Config["dns"], want) {
		t.Fatalf("dns after export %v, want %v", again.Config["dns"], want)
	}
}

// configureAwgAndVLESS writes the whole config for an Amnezia profile beside a
// VLESS one, in auto mode, the way a stored profile goes: Decode keeps the
// link, Configure parses it again.
func configureAwgAndVLESS(t *testing.T, conf string) (*Backend, map[string]interface{}) {
	t.Helper()
	b := &Backend{config: configfile.New(filepath.Join(t.TempDir(), "config.json"))}
	var profiles []profile.Profile
	for name, link := range map[string]string{"amnezia": conf, "vless": testVLESS} {
		decoded, err := b.Decode(link)
		if err != nil {
			t.Fatal(err)
		}
		decoded[0].Name = name
		profiles = append(profiles, decoded[0])
	}
	if err := b.Configure(engine.Configuration{Profiles: profiles, ActiveProfile: "amnezia", AutoSelect: true, Routing: routing.DefaultRoutingState()}); err != nil {
		t.Fatal(err)
	}
	cfg, err := b.config.Read()
	if err != nil {
		t.Fatal(err)
	}
	return b, cfg
}

func taggedIn(list interface{}, tag string) map[string]interface{} {
	items, _ := list.([]interface{})
	for _, it := range items {
		if m, ok := it.(map[string]interface{}); ok && m["tag"] == tag {
			return m
		}
	}
	return nil
}

// The names an Amnezia endpoint resolves itself go to the config's DNS through
// the endpoint; the TUN's DNS (fakeip, dns-direct) and the VLESS profile stay
// as they were, and the real core takes the whole config.
func TestAwgEndpointResolvesThroughItsTunnel(t *testing.T) {
	b, cfg := configureAwgAndVLESS(t, awgConfWithDNS("172.29.172.254"))
	ep := taggedIn(cfg["endpoints"], "p-amnezia")
	if ep == nil {
		t.Fatalf("no endpoint: %v", cfg["endpoints"])
	}
	if _, leaked := ep["dns"]; leaked {
		t.Fatal("the profile's dns reached the core, which refuses unknown fields")
	}
	resolver, _ := ep["destination_resolver"].(map[string]interface{})
	if resolver["server"] != "dns-in-p-amnezia" || resolver["strategy"] != "ipv4_only" {
		t.Fatalf("destination_resolver %v", ep["destination_resolver"])
	}
	dns := cfg["dns"].(map[string]interface{})
	server := taggedIn(dns["servers"], "dns-in-p-amnezia")
	want := map[string]interface{}{"tag": "dns-in-p-amnezia", "type": "udp", "server": "172.29.172.254", "detour": "p-amnezia"}
	if !reflect.DeepEqual(server, want) {
		t.Fatalf("tunnel resolver %v, want %v", server, want)
	}
	if taggedIn(dns["servers"], "fakeip-dns") == nil || dns["final"] != "dns-direct" {
		t.Fatalf("the TUN's DNS changed: %v", dns)
	}
	rules, _ := json.Marshal(dns["rules"])
	if strings.Contains(string(rules), "dns-in-") {
		t.Fatalf("a DNS rule names the tunnel resolver, TUN queries would leave fakeip: %s", rules)
	}
	vless := taggedIn(cfg["outbounds"], "p-vless")
	if vless == nil || vless["destination_resolver"] != nil || vless["domain_resolver"] != nil {
		t.Fatalf("VLESS outbound changed: %v", vless)
	}
	core := realCore(t)
	if out, err := exec.Command(core, "check", "-c", b.config.Path()).CombinedOutput(); err != nil {
		t.Fatalf("sing-box check: %v\n%s", err, out)
	}
}

// Without a DNS line the endpoint still resolves inside the tunnel, through
// Cloudflare, the Amnezia client's own default.
func TestAwgEndpointWithoutDNSLineUsesCloudflareInTheTunnel(t *testing.T) {
	_, cfg := configureAwgAndVLESS(t, pskAwgConf)
	server := taggedIn(cfg["dns"].(map[string]interface{})["servers"], "dns-in-p-amnezia")
	if server == nil || server["server"] != "1.1.1.1" || server["detour"] != "p-amnezia" {
		t.Fatalf("tunnel resolver %v", server)
	}
}

// A split tunnel that carries no resolver keeps the DNS rules: a server the
// endpoint cannot reach would fail every name.
func TestAwgSplitTunnelWithoutResolverKeepsRules(t *testing.T) {
	conf := strings.Replace(awgConfWithDNS("9.9.9.9"), "AllowedIPs = 0.0.0.0/0", "AllowedIPs = 10.0.0.0/8", 1)
	_, cfg := configureAwgAndVLESS(t, conf)
	if server := taggedIn(cfg["dns"].(map[string]interface{})["servers"], "dns-in-p-amnezia"); server != nil {
		t.Fatalf("tunnel resolver outside AllowedIPs: %v", server)
	}
	if ep := taggedIn(cfg["endpoints"], "p-amnezia"); ep["destination_resolver"] != nil {
		t.Fatalf("destination_resolver without a server: %v", ep)
	}
}

// The real core resolves the name a delay probe dials through the endpoint: the
// query to the config's DNS leaves as a packet connection of the endpoint.
// Offline the peer never answers, so only the path is checked, not an answer.
func TestRealCoreSendsEndpointLookupsIntoTheTunnel(t *testing.T) {
	core := realCore(t)
	_, cfg := configureAwgAndVLESS(t, awgConfWithDNS("9.9.9.9"))
	dir := t.TempDir()
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
	resp, err := http.Get(api + "/proxies/p-amnezia/delay?timeout=1500&url=http://cp.cloudflare.com/generate_204")
	if err == nil {
		resp.Body.Close()
	}
	logText, _ := os.ReadFile(filepath.Join(dir, "core.log"))
	log := string(logText)
	if !strings.Contains(log, "outbound packet connection to 9.9.9.9:53") {
		t.Fatalf("the endpoint's lookup did not go to the config's DNS through the endpoint:\n%s", log)
	}
}
