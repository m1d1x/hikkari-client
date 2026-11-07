package capacity

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"strings"
	"testing"
)

func awgFixture(t *testing.T, private, address, server, h1 string) string {
	t.Helper()
	config := "[Interface]\nPrivateKey = " + private + "\nAddress = " + address + "/32\nMTU = 1280\nJc = 4\nH1 = " + h1 + "\n[Peer]\nPublicKey = server-public\nAllowedIPs = 0.0.0.0/0\nEndpoint = " + server + ":31000\nPersistentKeepalive = 25\n"
	raw, err := json.Marshal(map[string]any{"containers": []any{map[string]any{"container": "amnezia-awg", "awg": map[string]any{"last_config": config}}}})
	if err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	binary.Write(&compressed, binary.BigEndian, uint32(len(raw)))
	writer := zlib.NewWriter(&compressed)
	writer.Write(raw)
	writer.Close()
	return "vpn://" + base64.RawURLEncoding.EncodeToString(compressed.Bytes())
}
func TestAmneziaCapacityIdentity(t *testing.T) {
	first := awgFixture(t, "probe-private", "10.66.0.2", "203.0.113.9", "11-12")
	user := awgFixture(t, "subscriber-private", "10.66.0.25", "203.0.113.9", "11-12") + "?hikkari-node=n%3Aone#User"
	key := Key(first)
	if len(key) != 64 || key != Key(user) {
		t.Fatal("same transport differs across peers")
	}
	// A fixed cross-repository contract vector prevents independent parser drift.
	if key != "f7cdc996b1b0fd4f5ef51df2fcc98d7a78cae2103c22bf2b644c5bf6d6f87efd" {
		t.Fatalf("contract vector: %s", key)
	}
	for _, changed := range []string{awgFixture(t, "p", "10.66.0.2", "203.0.113.10", "11-12"), awgFixture(t, "p", "10.66.0.2", "203.0.113.9", "13-14")} {
		if Key(changed) == key {
			t.Fatal("transport mutation reused measurements")
		}
	}
	for _, bad := range []string{"vpn://", "vpn://garbage", "vpn://" + strings.Repeat("A", (2<<20)+1)} {
		if Key(bad) != "" {
			t.Fatal("malformed document accepted")
		}
	}
}
