package xray

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"NekkoVPN/core/profile"
	"golang.org/x/net/proxy"
)

const fleetMasks = `[{"type":"salamander","password":"0123456789abcdef0123456789abcdef"},{"type":"noise","junk":[{"len":[8,16],"delay_ms":[0,1]}],"reset_secs":[0,0]}]`

func fleetLink(pin string) string {
	q := url.Values{"sni": {"fleet.test"}, "alpn": {"h3"}, "pinSHA256": {pin}, "finalmask": {base64.RawURLEncoding.EncodeToString([]byte(fleetMasks))}}
	return "hysteria2://test-hysteria-auth@127.0.0.1:443?" + q.Encode()
}
func TestFleetHysteriaPreservesPinAndEveryMask(t *testing.T) {
	pin := strings.Repeat("ab", 32)
	ob, err := outbound(fleetLink(pin), "test")
	if err != nil {
		t.Fatal(err)
	}
	stream := ob["streamSettings"].(object)
	if stream["tlsSettings"].(object)["pinnedPeerCertSha256"] != pin {
		t.Fatal("certificate pin lost")
	}
	masks := stream["finalmask"].(object)["udp"].([]interface{})
	if len(masks) != 2 || masks[1].(object)["type"] != "noise" {
		t.Fatal("Noise lost")
	}
	for _, bad := range []string{"", "xyz", strings.Repeat("ab", 31)} {
		if _, err := outbound(fleetLink(bad), "test"); err == nil {
			t.Fatal("invalid pin accepted")
		}
	}
	for _, raw := range []string{`[]`, `[{"type":"unknown"}]`, `[{"type":"noise","junk":[],"reset_secs":[0,0]}]`, `[{"type":"noise","junk":[{"len":[100,1],"delay_ms":[0,0]}],"reset_secs":[0,0]}]`, fleetMasks + `{}`} {
		if _, err := packetMasks(base64.RawURLEncoding.EncodeToString([]byte(raw))); err == nil {
			t.Fatalf("invalid masks accepted: %s", raw)
		}
	}
	if _, err := outbound("vless://id@127.0.0.1:443?finalmask="+base64.RawURLEncoding.EncodeToString([]byte(fleetMasks)), "test"); err == nil {
		t.Fatal("masks accepted outside Hysteria2")
	}
}

// Two actual Xray processes exercise the QUIC handshake, both masks and the
// certificate pin. The wrong-pin case must never reach the HTTP destination.
func TestRealFleetHysteriaPinAndMasks(t *testing.T) {
	bin := engineBinary(t)
	dir := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fleet.test"}, DNSNames: []string{"fleet.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := strings.Split(strings.TrimSpace(string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))), "\n")
	keyPEM := strings.Split(strings.TrimSpace(string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))), "\n")
	sum := sha256.Sum256(der)
	pin := hex.EncodeToString(sum[:])
	ports, err := AllocatePorts([]profile.Profile{{Name: "server"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	port := ports["server"]
	masks, err := packetMasks(base64.RawURLEncoding.EncodeToString([]byte(fleetMasks)))
	if err != nil {
		t.Fatal(err)
	}
	serverConfig := object{"log": object{"loglevel": "none"}, "inbounds": []interface{}{object{"listen": "127.0.0.1", "port": port, "protocol": "hysteria", "settings": object{"version": 2, "clients": []interface{}{object{"auth": "test-hysteria-auth"}}}, "streamSettings": object{"network": "hysteria", "security": "tls", "hysteriaSettings": object{"version": 2}, "tlsSettings": object{"alpn": []string{"h3"}, "certificates": []interface{}{object{"certificate": certPEM, "key": keyPEM}}}, "finalmask": object{"udp": masks}}}}, "outbounds": []interface{}{object{"protocol": "freedom", "settings": object{"finalRules": []interface{}{object{"action": "allow", "ip": []string{"127.0.0.1/32"}}}}}}}
	serverPath := filepath.Join(dir, "server.json")
	writeJSON(t, serverPath, serverConfig)
	if out, err := exec.Command(bin, "run", "-test", "-c", serverPath).CombinedOutput(); err != nil {
		t.Fatalf("server config: %s", out)
	}
	server := exec.Command(bin, "run", "-c", serverPath)
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Process.Kill(); _ = server.Wait() }()
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "pinned-hysteria") }))
	defer destination.Close()
	for _, tc := range []struct {
		name, pin string
		success   bool
	}{{"correct-pin", pin, true}, {"wrong-pin", strings.Repeat("00", 32), false}} {
		t.Run(tc.name, func(t *testing.T) {
			uri := strings.Replace(fleetLink(tc.pin), "127.0.0.1:443", fmt.Sprintf("127.0.0.1:%d", port), 1)
			profiles, err := Decode(uri)
			if err != nil {
				t.Fatal(err)
			}
			profiles[0].Name = "client"
			clientPorts, err := AllocatePorts(profiles, nil)
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := Compile(profiles, clientPorts)
			if err != nil {
				t.Fatal(err)
			}
			cfg["log"] = object{"loglevel": "none"}
			clientPath := filepath.Join(t.TempDir(), "client.json")
			writeJSON(t, clientPath, cfg)
			process := &Process{Binary: bin, Config: clientPath, Directory: dir}
			defer process.Stop()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := process.Start(ctx, clientPorts); err != nil {
				t.Fatal(err)
			}
			dialer, err := proxy.SOCKS5("tcp", fmtAddress(clientPorts["client"]), nil, proxy.Direct)
			if err != nil {
				t.Fatal(err)
			}
			client := &http.Client{Transport: &http.Transport{Dial: dialer.Dial}, Timeout: 2 * time.Second}
			defer client.CloseIdleConnections()
			var body []byte
			for attempt := 0; attempt < 3; attempt++ {
				resp, err := client.Get(destination.URL)
				if err != nil {
					if !tc.success {
						break
					}
					continue
				}
				body, _ = io.ReadAll(resp.Body)
				resp.Body.Close()
				break
			}
			if (string(body) == "pinned-hysteria") != tc.success {
				raw, _ := json.Marshal(cfg)
				t.Fatalf("TLS pin outcome incorrect (%s): %s", tc.name, raw)
			}
		})
	}
}
