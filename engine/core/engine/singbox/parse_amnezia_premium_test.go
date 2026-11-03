package singbox

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/json"
	"testing"
)

// The server link the Amnezia exchange produces, in the shape the gateway answers
// with: the AmneziaWG settings live in last_config, a JSON document in a string.
func TestParseAmneziaPremiumServerLink(t *testing.T) {
	ini := "[Interface]\nAddress = 10.8.1.2/32\nPrivateKey = 2DkSpTIwmYhJ2c2mC7dXvPqXbbxYcTyvJh1F7Zq3hF0=\nDNS = 1.1.1.1\n" +
		"Jc = 4\nJmin = 10\nJmax = 50\nS1 = 20\nS2 = 30\nS3 = 12\nS4 = 8\nH1 = 100\nH2 = 200\nH3 = 300\nH4 = 400\n\n" +
		"[Peer]\nPublicKey = 9jalV3EEBnVXb96Tl5mC1pBmRgIg0hYTAzSVrOMqqgc=\nAllowedIPs = 0.0.0.0/0\nEndpoint = 203.0.113.7:51820\nPersistentKeepalive = 25\n"
	inner, _ := json.Marshal(map[string]string{"config": ini, "hostName": "203.0.113.7"})
	document, _ := json.Marshal(map[string]any{
		"description":      "Amnezia Premium",
		"hostName":         "203.0.113.7",
		"defaultContainer": "amnezia-awg",
		"config_version":   2,
		"api_config":       map[string]string{"service_type": "amnezia-premium"},
		"containers": []any{map[string]any{
			"container": "amnezia-awg",
			"awg":       map[string]string{"last_config": string(inner), "port": "51820"},
		}},
	})
	var compressed bytes.Buffer
	compressed.Write([]byte{0, 0, 0, 0})
	writer := zlib.NewWriter(&compressed)
	writer.Write(document)
	writer.Close()

	draft, err := ParseURI("vpn://" + base64.RawURLEncoding.EncodeToString(compressed.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if draft.Label != "Amnezia Premium" {
		t.Errorf("label = %q", draft.Label)
	}
	if draft.Kind != "endpoint" || draft.Config["type"] != "awg" {
		t.Fatalf("kind = %q, type = %v", draft.Kind, draft.Config["type"])
	}
}
