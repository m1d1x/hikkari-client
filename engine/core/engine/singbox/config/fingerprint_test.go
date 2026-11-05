package configfile

import (
	"net"
	"path/filepath"
	"testing"
)

func fingerprintSpecs() []ProfileSpec {
	return []ProfileSpec{
		{Tag: "p-a", Kind: kindOutbound, Config: map[string]interface{}{"tag": "p-a", "type": "vless", "server": "a.example.com", "server_port": 443.0}},
		{Tag: "p-b", Kind: kindOutbound, Config: map[string]interface{}{"tag": "p-b", "type": "vless", "server": "b.example.com", "server_port": 443.0}},
	}
}

func TestFingerprintFollowsWhatTheConfigMeans(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "config.json"))
	build := func(active string, routing RoutingState, host HostNetwork) string {
		t.Helper()
		if err := store.RebuildActiveWithRouting(fingerprintSpecs(), active, false, nil, routing, host); err != nil {
			t.Fatal(err)
		}
		fingerprint, err := store.Fingerprint()
		if err != nil {
			t.Fatal(err)
		}
		return fingerprint
	}
	state := DefaultRoutingState()
	base := build("p-a", state, HostNetwork{})

	if got := build("p-a", state, HostNetwork{}); got != base {
		t.Fatal("the same input gave another fingerprint")
	}
	if got := build("p-b", state, HostNetwork{}); got != base {
		t.Fatal("selecting a profile changed the fingerprint: the plate switches inside the core")
	}
	if got := build("p-a", state, HostNetwork{DirectInterface: "en0", DirectDNS: "10.0.0.1"}); got != base {
		t.Fatal("the direct path chosen at connect changed the fingerprint")
	}
	if got := build("p-a", state, HostNetwork{}); got != base {
		t.Fatal("going back to the default direct path changed the fingerprint")
	}

	// Pinning writes the file after the fingerprint is taken, and is undone by
	// the next rebuild.
	if _, err := store.PinServerIPs(func(string) ([]net.IP, error) { return []net.IP{net.IPv4(203, 0, 113, 9)}, nil }); err != nil {
		t.Fatal(err)
	}
	if got := build("p-a", state, HostNetwork{}); got != base {
		t.Fatal("a pinned address survived into the fingerprint")
	}

	specs := fingerprintSpecs()[:1]
	if err := store.RebuildActiveWithRouting(specs, "p-a", false, nil, state, HostNetwork{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.Fingerprint(); got == base {
		t.Fatal("a profile set change did not change the fingerprint")
	}

	cfg, err := store.Read()
	if err != nil {
		t.Fatal(err)
	}
	route := cfg["route"].(map[string]interface{})
	route["rules"] = append([]interface{}{map[string]interface{}{"ip_cidr": []interface{}{"2000::/3"}, "action": "sniff"}}, route["rules"].([]interface{})...)
	if err := store.Write(cfg); err != nil {
		t.Fatal(err)
	}
	withRule, _ := store.Fingerprint()
	if err := store.RebuildActiveWithRouting(specs, "p-a", false, nil, state, HostNetwork{}); err != nil {
		t.Fatal(err)
	}
	if without, _ := store.Fingerprint(); withRule == without {
		t.Fatal("a routing rule did not change the fingerprint")
	}
}
