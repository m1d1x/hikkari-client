package diag

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"NekkoVPN/core/naive"
)

func TestNaiveDiagnosticsClassifyQUIC(t *testing.T) {
	raw := []byte(`{"outbounds":[{"tag":"p-h2","type":"naive","server":"127.0.0.1","server_port":443,"quic":false},{"tag":"p-h3","type":"naive","server":"127.0.0.1","server_port":443,"quic":true,"tls":{"enabled":true}}]}`)
	targets, err := reachTargets(raw)
	if err != nil || len(targets) != 2 || targets[0].UDP || !targets[1].UDP {
		t.Fatalf("reach: %+v %v", targets, err)
	}
	load, err := loadTargets(raw)
	if err != nil || len(load) != 2 {
		t.Fatalf("load: %+v %v", load, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, dead := addrDead(ctx, load[1], net.ParseIP("127.0.0.1")); dead || ctx.Err() != nil {
		t.Fatal("H3 must not be gated by a TCP probe")
	}
}

// Real Chromium/Cronet outbound and sing-box Naive server on loopback. This
// verifies packaging and the dl/burst/after path; it makes no claim about DPI.
func TestNaiveLoadThroughRealCore(t *testing.T) {
	binary := os.Getenv("HIKKARI_TEST_CORE")
	if binary == "" {
		t.Skip("HIKKARI_TEST_CORE is not set")
	}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/down" {
			_, _ = w.Write(make([]byte, loadBytes))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer origin.Close()
	// A local test CA is passed explicitly to Cronet; verification stays on.
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}))
	key, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}))
	iface := "lo"
	if runtime.GOOS == "darwin" {
		iface = "lo0"
	}
	if runtime.GOOS == "windows" {
		iface = ""
	}
	for _, quic := range []bool{false, true} {
		t.Run(fmt.Sprintf("quic=%v", quic), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			port := listener.Addr().(*net.TCPAddr).Port
			listener.Close()
			network := "tcp"
			if quic {
				network = "udp"
			}
			config := map[string]interface{}{
				"log": map[string]interface{}{"level": "info", "disabled": false},
				"inbounds": []interface{}{map[string]interface{}{
					"type": "naive", "listen": "127.0.0.1", "listen_port": port, "network": network,
					"users": []interface{}{map[string]interface{}{"username": "test", "password": "test-secret"}},
					"tls":   map[string]interface{}{"enabled": true, "certificate": []string{certPEM}, "key": []string{keyPEM}},
				}},
				"outbounds": []interface{}{map[string]interface{}{"type": "direct", "tag": "direct"}},
			}
			raw, _ := json.Marshal(config)
			path := filepath.Join(t.TempDir(), "server.json")
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			logFile, err := os.Create(filepath.Join(t.TempDir(), "server.log"))
			if err != nil {
				t.Fatal(err)
			}
			defer logFile.Close()
			cmd := exec.CommandContext(ctx, binary, "run", "-c", path)
			cmd.Stdout, cmd.Stderr = logFile, logFile
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				cancel()
				_ = cmd.Wait()
				if t.Failed() {
					data, _ := os.ReadFile(logFile.Name())
					t.Logf("server: %s", data)
				}
			}()
			ready := false
			deadline := time.Now().Add(15 * time.Second) // first Rosetta launch translates the x86 core
			for time.Now().Before(deadline) && ctx.Err() == nil {
				data, _ := os.ReadFile(logFile.Name())
				if strings.Contains(string(data), "sing-box started") {
					ready = true
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if !ready {
				t.Fatal("Naive server did not start")
			}
			scheme := "naive+https"
			if quic {
				scheme = "naive+quic"
			}
			u, _ := url.Parse(scheme + "://test:test-secret@127.0.0.1:" + strconv.Itoa(port))
			link, err := naive.Parse(u)
			if err != nil {
				t.Fatal(err)
			}
			ob := link.Outbound()
			ob["tls"].(map[string]interface{})["certificate"] = []string{certPEM}
			target := loadTarget{Tag: "p-naive", Proto: "naive", Outbound: ob}
			plan := loadPlan{DownloadURL: origin.URL + "/down", CheckURL: origin.URL, Bytes: loadBytes,
				Burst: 8, DownWait: 10 * time.Second, BurstWait: 5 * time.Second, AfterWait: 2 * time.Second}
			line := loadLine(ctx, binary, target, iface, plan)
			t.Log(line)
			if !strings.Contains(line, fmt.Sprintf("dl=%d/%d", loadBytes, loadBytes)) || !strings.Contains(line, "end=ok burst=8/8") || !strings.Contains(line, "after=3/3") {
				t.Fatal("Naive load did not pass")
			}
			plan.Burst, plan.DownWait, plan.BurstWait, plan.AfterWait = 1, time.Second, time.Second, time.Second
			for _, failure := range []string{"password", "certificate"} {
				ob["password"] = "test-secret"
				if failure == "password" {
					ob["password"] = "wrong-secret"
				} else {
					delete(ob["tls"].(map[string]interface{}), "certificate")
				}
				line = loadLine(ctx, binary, target, iface, plan)
				if !strings.Contains(line, "dl=0/") || !strings.Contains(line, "burst=0/1") || !strings.Contains(line, "after=0/3") || strings.Contains(line, "secret") {
					t.Fatalf("bad %s read as healthy or leaked: %s", failure, line)
				}
			}
		})
	}
}
