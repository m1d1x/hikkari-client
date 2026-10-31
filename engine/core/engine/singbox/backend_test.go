package singbox

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"NekkoVPN/core/engine"
	configfile "NekkoVPN/core/engine/singbox/config"
	"NekkoVPN/core/profile"
	"NekkoVPN/core/routing"
)

func makeAmneziaVPNLink(payload []byte, claimedLen uint32) string {
	var compressed bytes.Buffer
	zw := zlib.NewWriter(&compressed)
	_, _ = zw.Write(payload)
	_ = zw.Close()

	hdr := make([]byte, 4)
	binary.BigEndian.PutUint32(hdr, claimedLen)

	combined := append(hdr, compressed.Bytes()...)
	return "vpn://" + base64.RawURLEncoding.EncodeToString(combined)
}

func TestAmneziaVPN_AWG(t *testing.T) {
	innerIni := `[Interface]
PrivateKey = aaaa
Address = 10.0.0.2/32

[Peer]
PublicKey = bbbb
Endpoint = 198.51.100.1:51820
AllowedIPs = 0.0.0.0/0
`
	lastCfgJSON, err := json.Marshal(map[string]interface{}{
		"config": innerIni,
	})
	if err != nil {
		t.Fatal(err)
	}

	amneziaJSON, err := json.Marshal(map[string]interface{}{
		"hostName":    "198.51.100.1",
		"description": "Amnezia AWG Test",
		"defaultContainer": "amnezia-awg",
		"containers": []interface{}{
			map[string]interface{}{
				"container": "amnezia-awg",
				"awg": map[string]interface{}{
					"last_config": string(lastCfgJSON),
					"Jc":          4,
					"Jmin":        40,
					"Jmax":        70,
					"S1":          15,
					"S2":          35,
					"H1":          "1",
					"H2":          "2",
					"H3":          "3",
					"H4":          "4",
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	uri := makeAmneziaVPNLink(amneziaJSON, uint32(len(amneziaJSON)))
	draft, err := ParseURI(uri)
	if err != nil {
		t.Fatalf("ParseURI failed: %v", err)
	}

	if draft.Kind != profile.KindEndpoint {
		t.Fatalf("want KindEndpoint, got %s", draft.Kind)
	}
	if draft.Label != "Amnezia AWG Test" {
		t.Fatalf("want label 'Amnezia AWG Test', got %q", draft.Label)
	}
	if draft.Config["type"] != "awg" {
		t.Fatalf("want config type 'awg', got %v", draft.Config["type"])
	}
	if draft.Config["jc"] != 4 || draft.Config["jmin"] != 40 || draft.Config["jmax"] != 70 {
		t.Fatalf("awg jc/jmin/jmax mismatch: %v", draft.Config)
	}
	if draft.Config["s1"] != 15 || draft.Config["s2"] != 35 {
		t.Fatalf("awg s1/s2 mismatch: %v", draft.Config)
	}
	if draft.Config["h1"] != "1" || draft.Config["h4"] != "4" {
		t.Fatalf("awg h1/h4 mismatch: %v", draft.Config)
	}

	peers, ok := draft.Config["peers"].([]interface{})
	if !ok || len(peers) == 0 {
		t.Fatalf("missing peers in awg draft: %v", draft.Config)
	}
	peer := peers[0].(map[string]interface{})
	if peer["address"] != "198.51.100.1" || peer["port"] != 51820 {
		t.Fatalf("peer address mismatch: %v", peer)
	}
}

func TestAmneziaVPN_XrayVLESSReality(t *testing.T) {
	inbounds := []interface{}{
		map[string]interface{}{
			"port":     443,
			"protocol": "vless",
			"settings": map[string]interface{}{
				"clients": []interface{}{
					map[string]interface{}{
						"id":   "00000000-0000-0000-0000-000000000001",
						"flow": "xtls-rprx-vision",
					},
				},
			},
			"streamSettings": map[string]interface{}{
				"network":  "tcp",
				"security": "reality",
				"realitySettings": map[string]interface{}{
					"dest":        "dl.google.com:443",
					"serverNames": []interface{}{"dl.google.com"},
					"publicKey":   "pubKeyReality123",
					"shortIds":    []interface{}{"0123456789abcdef"},
				},
			},
		},
	}
	lastCfgJSON, err := json.Marshal(map[string]interface{}{
		"inbounds": inbounds,
	})
	if err != nil {
		t.Fatal(err)
	}

	amneziaJSON, err := json.Marshal(map[string]interface{}{
		"hostName":    "198.51.100.2",
		"description": "Amnezia Reality",
		"defaultContainer": "amnezia-xray",
		"containers": []interface{}{
			map[string]interface{}{
				"container": "amnezia-xray",
				"xray": map[string]interface{}{
					"last_config": string(lastCfgJSON),
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	uri := makeAmneziaVPNLink(amneziaJSON, uint32(len(amneziaJSON)))
	draft, err := ParseURI(uri)
	if err != nil {
		t.Fatalf("ParseURI failed: %v", err)
	}

	if draft.Kind != profile.KindOutbound {
		t.Fatalf("want KindOutbound, got %s", draft.Kind)
	}
	if draft.Label != "Amnezia Reality" {
		t.Fatalf("want label 'Amnezia Reality', got %q", draft.Label)
	}
	if draft.Config["type"] != "vless" {
		t.Fatalf("want type 'vless', got %v", draft.Config["type"])
	}
	if draft.Config["server"] != "198.51.100.2" || draft.Config["server_port"] != 443 {
		t.Fatalf("server mismatch: %v", draft.Config)
	}
	if draft.Config["uuid"] != "00000000-0000-0000-0000-000000000001" {
		t.Fatalf("uuid mismatch: %v", draft.Config)
	}
	if draft.Config["flow"] != "xtls-rprx-vision" {
		t.Fatalf("flow mismatch: %v", draft.Config)
	}
	tls, ok := draft.Config["tls"].(map[string]interface{})
	if !ok {
		t.Fatalf("tls block missing: %v", draft.Config)
	}
	if tls["server_name"] != "dl.google.com" {
		t.Fatalf("sni mismatch: %v", tls)
	}
	utls, ok := tls["utls"].(map[string]interface{})
	if !ok || utls["fingerprint"] != "chrome" {
		t.Fatalf("utls fingerprint mismatch: %v", tls)
	}
	reality, ok := tls["reality"].(map[string]interface{})
	if !ok || reality["public_key"] != "pubKeyReality123" || reality["short_id"] != "0123456789abcdef" {
		t.Fatalf("reality params mismatch: %v", tls)
	}
}

func TestAmneziaVPN_Shadowsocks(t *testing.T) {
	lastCfgJSON, err := json.Marshal(map[string]interface{}{
		"port":     8388,
		"method":   "aes-256-gcm",
		"password": "secretpassword",
	})
	if err != nil {
		t.Fatal(err)
	}

	amneziaJSON, err := json.Marshal(map[string]interface{}{
		"hostName":    "198.51.100.3",
		"description": "Amnezia SS",
		"defaultContainer": "amnezia-shadowsocks",
		"containers": []interface{}{
			map[string]interface{}{
				"container": "amnezia-shadowsocks",
				"shadowsocks": map[string]interface{}{
					"last_config": string(lastCfgJSON),
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	uri := makeAmneziaVPNLink(amneziaJSON, uint32(len(amneziaJSON)))
	draft, err := ParseURI(uri)
	if err != nil {
		t.Fatalf("ParseURI failed: %v", err)
	}

	if draft.Kind != profile.KindOutbound {
		t.Fatalf("want KindOutbound, got %s", draft.Kind)
	}
	if draft.Label != "Amnezia SS" {
		t.Fatalf("want label 'Amnezia SS', got %q", draft.Label)
	}
	if draft.Config["type"] != "shadowsocks" {
		t.Fatalf("want type 'shadowsocks', got %v", draft.Config["type"])
	}
	if draft.Config["server"] != "198.51.100.3" || draft.Config["server_port"] != 8388 {
		t.Fatalf("server host/port mismatch: %v", draft.Config)
	}
	if draft.Config["method"] != "aes-256-gcm" || draft.Config["password"] != "secretpassword" {
		t.Fatalf("method/password mismatch: %v", draft.Config)
	}
}

func TestAmneziaVPN_FallbacksAndPlainJSON(t *testing.T) {
	// 1. Plain uncompressed JSON inside base64
	amneziaJSON := `{"hostName":"198.51.100.4","description":"Plain JSON","defaultContainer":"amnezia-shadowsocks","containers":[{"container":"amnezia-shadowsocks","shadowsocks":{"port":8388,"method":"chacha20-poly1305","password":"pw"}}]}`
	plainURI := "vpn://" + base64.RawURLEncoding.EncodeToString([]byte(amneziaJSON))
	draft, err := ParseURI(plainURI)
	if err != nil {
		t.Fatalf("failed plain JSON vpn://: %v", err)
	}
	if draft.Label != "Plain JSON" || draft.Config["method"] != "chacha20-poly1305" {
		t.Fatalf("unexpected draft from plain JSON: %v", draft)
	}

	// 2. Raw zlib stream without Qt 4-byte header
	var compressed bytes.Buffer
	zw := zlib.NewWriter(&compressed)
	_, _ = zw.Write([]byte(amneziaJSON))
	_ = zw.Close()
	rawZlibURI := "vpn://" + base64.RawURLEncoding.EncodeToString(compressed.Bytes())
	draftZlib, err := ParseURI(rawZlibURI)
	if err != nil {
		t.Fatalf("failed raw zlib vpn://: %v", err)
	}
	if draftZlib.Label != "Plain JSON" {
		t.Fatalf("unexpected draft from raw zlib: %v", draftZlib)
	}
}

func TestAmneziaVPN_ProtectionAgainstZipBombAndCorruptedData(t *testing.T) {
	// 1. Corrupted base64
	if _, err := ParseURI("vpn://not-valid-base64!!!"); err == nil {
		t.Fatal("expected error for corrupted base64, got nil")
	}

	// 2. Corrupted payload
	corrupted := "vpn://" + base64.RawURLEncoding.EncodeToString([]byte("random junk that is not zlib or json"))
	if _, err := ParseURI(corrupted); err == nil {
		t.Fatal("expected error for corrupted payload, got nil")
	}

	// 3. Zip bomb: claimed length in Qt header > 1 MiB
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	_, _ = zw.Write([]byte(`{"hostName":"1.2.3.4"}`))
	_ = zw.Close()
	hdr := make([]byte, 4)
	binary.BigEndian.PutUint32(hdr, 2*1024*1024) // 2 MiB > 1 MiB limit
	hugeClaimURI := "vpn://" + base64.RawURLEncoding.EncodeToString(append(hdr, buf.Bytes()...))
	if _, err := ParseURI(hugeClaimURI); err == nil || !strings.Contains(err.Error(), "1 МиБ") {
		t.Fatalf("expected 1 MiB limit error for huge claimed length, got: %v", err)
	}

	// 4. Zip bomb: claimed length is small (e.g. 100), but compressed data expands to > 1 MiB
	var bombBuf bytes.Buffer
	zwBomb := zlib.NewWriter(&bombBuf)
	largeData := bytes.Repeat([]byte("A"), 2*1024*1024) // 2 MiB of uncompressed data
	_, _ = zwBomb.Write(largeData)
	_ = zwBomb.Close()

	binary.BigEndian.PutUint32(hdr, 100) // fake small size in Qt header
	bombURI := "vpn://" + base64.RawURLEncoding.EncodeToString(append(hdr, bombBuf.Bytes()...))
	if _, err := ParseURI(bombURI); err == nil || !strings.Contains(err.Error(), "1 МиБ") {
		t.Fatalf("expected 1 MiB limit error for zip bomb expansion, got: %v", err)
	}
}

func TestAmneziaVPN_BackendDecodeAndConfigure(t *testing.T) {
	dir := t.TempDir()
	b := &Backend{config: configfile.New(filepath.Join(dir, "config.json"))}

	innerIni := `[Interface]
PrivateKey = aaaa
Address = 10.0.0.2/32

[Peer]
PublicKey = bbbb
Endpoint = 198.51.100.1:51820
AllowedIPs = 0.0.0.0/0
`
	lastCfgJSON, _ := json.Marshal(map[string]interface{}{"config": innerIni})
	amneziaJSON, _ := json.Marshal(map[string]interface{}{
		"hostName":         "198.51.100.1",
		"description":      "Amnezia E2E",
		"defaultContainer": "amnezia-awg",
		"containers": []interface{}{
			map[string]interface{}{
				"container": "amnezia-awg",
				"awg": map[string]interface{}{
					"last_config": string(lastCfgJSON),
					"Jc":          3,
					"Jmin":        20,
					"Jmax":        50,
					"S1":          10,
					"S2":          20,
					"H1":          "1",
					"H2":          "2",
					"H3":          "3",
					"H4":          "4",
				},
			},
		},
	})
	uri := makeAmneziaVPNLink(amneziaJSON, uint32(len(amneziaJSON)))
	profiles, err := b.Decode(uri)
	if err != nil {
		t.Fatalf("b.Decode failed: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("want 1 profile, got %d", len(profiles))
	}
	p := profiles[0]
	p.Name = "amnezia-p1"
	if err := b.Configure(engine.Configuration{Profiles: []profile.Profile{p}, ActiveProfile: "amnezia-p1", Routing: routing.DefaultRoutingState()}); err != nil {
		t.Fatalf("b.Configure failed: %v", err)
	}
}

func TestPortableProfilesCompileWithoutChangingLegacyConfig(t *testing.T) {
	dir := t.TempDir()
	b := &Backend{config: configfile.New(filepath.Join(dir, "config.json"))}
	old := profile.Profile{Name: "legacy", Label: "Legacy", Kind: profile.KindOutbound, Config: map[string]interface{}{"type": "vless", "server": "127.0.0.1", "server_port": 443, "uuid": "id", "custom_legacy_option": "preserve"}}
	normalized, err := b.Normalize(old)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Connection == nil || normalized.Config["custom_legacy_option"] != "preserve" {
		t.Fatal("legacy migration lost native options")
	}
	fresh, err := b.Decode("vless://id@127.0.0.1:8443?security=tls&sni=example.com#New")
	if err != nil {
		t.Fatal(err)
	}
	fresh[0].Name = "portable"
	if fresh[0].Config != nil || fresh[0].Kind != "" {
		t.Fatal("new profile is engine-native")
	}
	before, _ := json.Marshal(old.Config)
	if err := b.Configure(engine.Configuration{Profiles: []profile.Profile{normalized, fresh[0]}, ActiveProfile: "portable", Routing: routing.DefaultRoutingState()}); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(old.Config)
	if string(before) != string(after) {
		t.Fatal("compiler mutated a stored legacy profile")
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, v := range cfg["outbounds"].([]interface{}) {
		if v.(map[string]interface{})["type"] == "vless" {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("compiled %d servers, want legacy and portable", count)
	}
}
