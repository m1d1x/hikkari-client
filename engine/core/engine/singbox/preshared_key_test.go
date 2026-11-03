package singbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"NekkoVPN/core/engine"
	configfile "NekkoVPN/core/engine/singbox/config"
	"NekkoVPN/core/profile"
	"NekkoVPN/core/routing"
)

const (
	testPSK     = "FbVDy8GTzCHo0ptp3ADUqwMMTk5PcJDhWo6LSgmT8hA="
	pskAwgConf  = "[Interface]\nPrivateKey = 6Mf9KmZ3Nn4bq2oXQyQbF0jv3bB4i0cVZ0Xh1q3xXkE=\nAddress = 10.8.1.7/32\nJc = 4\nJmin = 40\nJmax = 70\nS1 = 33\nS2 = 57\nH1 = 11\nH2 = 12\nH3 = 13\nH4 = 14\n\n[Peer]\nPublicKey = lTmjpHaGYBYQ8T5oogAWheEzhjCnzFwvoaZYQevHMgQ=\nPresharedKey = " + testPSK + "\nAllowedIPs = 0.0.0.0/0\nEndpoint = 203.0.113.9:41202\nPersistentKeepalive = 25\n"
	realCoreRel = "../../../../archive/sing-box"
)

// realCore is the core built as CI builds it, or a skip where it is not.
func realCore(t *testing.T) string {
	t.Helper()
	core, err := filepath.Abs(realCoreRel)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(core); err != nil {
		t.Skip("no sing-box binary at archive/sing-box")
	}
	return core
}

// Amnezia Premium hands out configs with a PresharedKey (tester-002, 04.11). The
// profile goes through the path a stored one takes: Decode keeps the link,
// Configure parses it again and writes the whole config, which the real core
// must accept.
func TestPresharedKeyProfileIsAcceptedByTheRealCore(t *testing.T) {
	core := realCore(t)
	dir := t.TempDir()
	b := &Backend{config: configfile.New(filepath.Join(dir, "config.json"))}
	decoded, err := b.Decode(pskAwgConf)
	if err != nil {
		t.Fatal(err)
	}
	p := decoded[0]
	p.Name = "amnezia"
	if !strings.Contains(p.Connection.URI, "PresharedKey = "+testPSK) {
		t.Fatalf("stored link lost the key:\n%s", p.Connection.URI)
	}
	if err := b.Configure(engine.Configuration{Profiles: []profile.Profile{p}, ActiveProfile: "amnezia", Routing: routing.DefaultRoutingState()}); err != nil {
		t.Fatal(err)
	}
	cfg, err := b.config.Read()
	if err != nil {
		t.Fatal(err)
	}
	peer := cfg["endpoints"].([]interface{})[0].(map[string]interface{})["peers"].([]interface{})[0].(map[string]interface{})
	if peer["pre_shared_key"] != testPSK {
		t.Fatalf("peer as written: %v", peer)
	}
	if out, err := exec.Command(core, "check", "-c", b.config.Path()).CombinedOutput(); err != nil {
		t.Fatalf("sing-box check: %v\n%s", err, out)
	}
}

// Export must give back what import took: the key, and the first-packet
// generator's request that replaces I1.
func TestAwgExportKeepsPresharedKeyAndMasquerade(t *testing.T) {
	conf := strings.Replace(pskAwgConf, "H4 = 14\n", "H4 = 14\nIp = quic\nId = www.gstatic.com\nIb = chrome\n", 1)
	first, err := ParseURI(conf)
	if err != nil {
		t.Fatal(err)
	}
	link, err := ExportURI(profile.Profile{Kind: first.Kind, Config: first.Config})
	if err != nil {
		t.Fatal(err)
	}
	again, err := ParseURI(link)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"ip", "id", "ib"} {
		if again.Config[k] != first.Config[k] || first.Config[k] == nil {
			t.Errorf("%s: imported %v, after export %v", k, first.Config[k], again.Config[k])
		}
	}
	peer := again.Config["peers"].([]interface{})[0].(map[string]interface{})
	if peer["pre_shared_key"] != testPSK {
		t.Errorf("peer after export: %v", peer)
	}
}
