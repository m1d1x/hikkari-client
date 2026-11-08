package singbox

import (
	"encoding/json"
	"testing"

	"NekkoVPN/core/capacity"
)

// A subscription's link is kept as given: its capacity key is the one the hub
// keyed the probers' measurements by. Re-exported, an AmneziaWG link became a
// WireGuard one and its profile never showed a speed.
func TestDecodeKeepsTheLinkItWasGiven(t *testing.T) {
	conf := "[Interface]\nPrivateKey = subscriber\nAddress = 10.66.0.25/32\nMTU = 1280\nJc = 4\nJmin = 40\nJmax = 70\nS1 = 15\nS2 = 35\nH1 = 1\nH2 = 2\nH3 = 3\nH4 = 4\n\n[Peer]\nPublicKey = server\nAllowedIPs = 0.0.0.0/0\nEndpoint = 198.51.100.1:31000\nPersistentKeepalive = 25\n"
	doc, err := json.Marshal(map[string]any{
		"description":      "амстердам-001",
		"hostName":         "198.51.100.1",
		"defaultContainer": "amnezia-awg",
		"containers":       []any{map[string]any{"container": "amnezia-awg", "awg": map[string]any{"last_config": conf}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, link := range []string{
		makeAmneziaVPNLink(doc, uint32(len(doc))) + "?hikkari-node=n%3Ad20957fe#амстердам-001",
		"vless://11111111-2222-3333-4444-555555555555@198.51.100.2:443?encryption=none&flow=xtls-rprx-vision&fp=chrome&hikkari-node=n%3Aabc&pbk=AAAA&security=reality&sid=01&sni=www.samsung.com&type=tcp#рига-001",
	} {
		profiles, err := (&Backend{}).Decode(link)
		if err != nil {
			t.Fatalf("decode %.20s: %v", link, err)
		}
		p := profiles[0]
		if p.Connection == nil || p.Connection.URI != link {
			t.Fatalf("stored link = %v, want the one given", p.Connection)
		}
		if capacity.Key(p.Connection.URI) != capacity.Key(link) || capacity.Key(link) == "" {
			t.Fatal("the stored link's capacity key differs from the subscription's")
		}
		if p.Connection.Host == "" || p.Connection.Port == 0 || p.Connection.Protocol == "" {
			t.Fatalf("connection = %+v, want host, port and protocol", p.Connection)
		}
		// The runtime builds its config from the stored link.
		if _, err := native(p); err != nil {
			t.Fatalf("stored link does not parse back: %v", err)
		}
	}
}
