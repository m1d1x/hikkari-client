package xray

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"NekkoVPN/core/profile"
	"golang.org/x/net/proxy"
)

func engineBinary(t *testing.T) string {
	t.Helper()
	p := os.Getenv("HIKKARI_XRAY_BINARY")
	if p == "" {
		t.Skip("set HIKKARI_XRAY_BINARY for real Xray integration")
	}
	return p
}
func writeJSON(t *testing.T, path string, value interface{}) {
	t.Helper()
	raw, e := json.Marshal(value)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
}
func TestRejectUnsupportedWithoutDowngrade(t *testing.T) {
	for _, uri := range []string{
		"wg://key@127.0.0.1:443", "vless://id@127.0.0.1:443?type=unknown",
		"vless://id@127.0.0.1:443?plugin=ignored", "vless://id@127.0.0.1:443?security=reality",
		"vless://id@127.0.0.1:443?security=tls&allowInsecure=1",
		"vless://id@127.0.0.1:443?serviceName=ignored", "vless://id@127.0.0.1:443?packetEncoding=packetaddr",
	} {
		if _, err := Decode(uri); err == nil {
			t.Errorf("accepted unsupported URI %s", uri)
		}
	}
}
func TestPortableConnectionRetainsParameters(t *testing.T) {
	uri := "vless://11111111-2222-3333-4444-555555555555@example.test:443?security=tls&type=xhttp&host=cover.test&path=%2Fsecret&mode=packet-up#Server"
	ps, err := Decode(uri)
	if err != nil {
		t.Fatal(err)
	}
	if ps[0].Connection.URI != uri || ps[0].Config != nil || ps[0].Kind != "" {
		t.Fatal("portable input changed")
	}
	ob, err := outbound(uri, "one")
	if err != nil {
		t.Fatal(err)
	}
	stream := ob["streamSettings"].(object)
	if stream["xhttpSettings"].(object)["path"] != "/secret" {
		t.Fatal("lost transport path")
	}
}
func TestPinnedXrayAcceptsSupportedProtocols(t *testing.T) {
	bin := engineBinary(t)
	uris := []string{
		"vless://11111111-2222-3333-4444-555555555555@127.0.0.1:443",
		"vless://11111111-2222-3333-4444-555555555555@127.0.0.1:443?security=tls&type=ws&host=example.test&path=%2Fws",
		"vless://11111111-2222-3333-4444-555555555555@127.0.0.1:443?security=tls&type=grpc&serviceName=test",
		"vless://11111111-2222-3333-4444-555555555555@127.0.0.1:443?security=tls&type=xhttp&mode=packet-up&path=%2Ftest&extra=%7B%22noSSEHeader%22%3Atrue%7D",
		"vless://11111111-2222-3333-4444-555555555555@127.0.0.1:443?security=reality&pbk=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&sid=01234567&flow=xtls-rprx-vision",
		"trojan://secret@127.0.0.1:443?security=tls&sni=example.test",
		"ss://YWVzLTI1Ni1nY206c2VjcmV0@127.0.0.1:8388",
		"hy2://secret@127.0.0.1:443?sni=example.test",
		"hysteria2://secret@127.0.0.1:443?sni=example.test&obfs=salamander&obfs-password=masked",
	}
	for _, uri := range uris {
		t.Run(strings.Split(strings.Split(uri, "?")[0], "://")[0], func(t *testing.T) {
			ps, err := Decode(uri)
			if err != nil {
				t.Fatal(err)
			}
			ps[0].Name = "server"
			ports, err := AllocatePorts(ps, nil)
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := Compile(ps, ports)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "xray.json")
			writeJSON(t, path, cfg)
			if out, err := exec.Command(bin, "run", "-test", "-c", path).CombinedOutput(); err != nil {
				t.Fatalf("Xray config failed: %s", out)
			}
		})
	}
}
func TestRealVLESSTransportAndStop(t *testing.T) {
	bin := engineBinary(t)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "through-real-xray")
	}))
	defer target.Close()
	serverProfiles := []profile.Profile{{Name: "fixture"}}
	serverPorts, err := AllocatePorts(serverProfiles, nil)
	if err != nil {
		t.Fatal(err)
	}
	port := serverPorts["fixture"]
	dir := t.TempDir()
	serverConfig := object{"log": object{"loglevel": "error"}, "inbounds": []interface{}{object{"listen": "127.0.0.1", "port": port, "protocol": "vless", "settings": object{"clients": []interface{}{object{"id": "11111111-2222-3333-4444-555555555555"}}, "decryption": "none"}}}, "outbounds": []interface{}{object{"protocol": "freedom", "settings": object{"finalRules": []interface{}{object{"action": "allow", "ip": []string{"127.0.0.1/32"}}}}}}}
	path := filepath.Join(dir, "server.json")
	writeJSON(t, path, serverConfig)
	server := exec.Command(bin, "run", "-c", path)
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Process.Kill(); _ = server.Wait() }()
	ps, err := Decode(fmt.Sprintf("vless://11111111-2222-3333-4444-555555555555@127.0.0.1:%d#Local", port))
	if err != nil {
		t.Fatal(err)
	}
	ps[0].Name = "local"
	ports, err := AllocatePorts(ps, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Compile(ps, ports)
	if err != nil {
		t.Fatal(err)
	}
	cfg["log"] = object{"loglevel": "debug", "error": filepath.Join(dir, "client.log"), "access": filepath.Join(dir, "access.log")}
	configPath := filepath.Join(dir, "client.json")
	writeJSON(t, configPath, cfg)
	process := &Process{Binary: bin, Config: configPath, Directory: dir}
	defer process.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := process.Start(ctx, ports); err != nil {
		t.Fatal(err)
	}
	dialer, err := proxy.SOCKS5("tcp", fmtAddress(ports["local"]), nil, proxy.Direct)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{Dial: dialer.Dial}, Timeout: 3 * time.Second}
	var body []byte
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		resp, e := client.Get(target.URL)
		if e != nil {
			t.Logf("request error: %v", e)
			continue
		}
		body, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
		break
	}
	client.CloseIdleConnections()
	if string(body) != "through-real-xray" {
		logs, _ := os.ReadFile(filepath.Join(dir, "client.log"))
		access, _ := os.ReadFile(filepath.Join(dir, "access.log"))
		t.Fatalf("no real transport response: %q\n%s\n%s", body, logs, access)
	}
	testUDPThroughSOCKS(t, ports["local"])
	if err := process.Stop(); err != nil {
		t.Fatal(err)
	}
	if process.IsRunning() {
		t.Fatal("transport survived Stop")
	}
}
func fmtAddress(port int) string { return fmt.Sprintf("127.0.0.1:%d", port) }

func TestUnknownLegacyOptionsFailExplicitly(t *testing.T) {
	ps, err := Decode("vless://11111111-2222-3333-4444-555555555555@127.0.0.1:443")
	if err != nil {
		t.Fatal(err)
	}
	ps[0].Name = "legacy"
	ps[0].Config = object{"type": "vless", "connect_timeout": "7s"}
	if _, err := Compile(ps, map[string]int{"legacy": 12345}); err == nil {
		t.Fatal("legacy native option silently ignored")
	}
	if ps[0].Config["connect_timeout"] != "7s" {
		t.Fatal("native option lost")
	}
}

func testUDPThroughSOCKS(t *testing.T, port int) {
	t.Helper()
	echo, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		buf := make([]byte, 1024)
		n, addr, err := echo.ReadFrom(buf)
		if err == nil {
			_, _ = echo.WriteTo(buf[:n], addr)
		}
	}()
	conn, err := net.DialTimeout("tcp4", fmtAddress(port), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = conn.Write([]byte{5, 1, 0})
	ack := make([]byte, 2)
	if _, err = io.ReadFull(conn, ack); err != nil || ack[1] != 0 {
		t.Fatalf("SOCKS greeting: %v", err)
	}
	_, _ = conn.Write([]byte{5, 3, 0, 1, 127, 0, 0, 1, 0, 0})
	reply := make([]byte, 10)
	if _, err = io.ReadFull(conn, reply); err != nil || reply[1] != 0 || reply[3] != 1 {
		t.Fatalf("SOCKS UDP association: %v %v", reply, err)
	}
	relay := &net.UDPAddr{IP: net.IP(reply[4:8]), Port: int(binary.BigEndian.Uint16(reply[8:]))}
	udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	_ = udp.SetDeadline(time.Now().Add(3 * time.Second))
	header := []byte{0, 0, 0, 1, 127, 0, 0, 1, 0, 0}
	binary.BigEndian.PutUint16(header[8:], uint16(echo.LocalAddr().(*net.UDPAddr).Port))
	payload := append(header, []byte("real-xray-udp")...)
	if _, err = udp.WriteToUDP(payload, relay); err != nil {
		t.Fatal(err)
	}
	result := make([]byte, 1024)
	n, _, err := udp.ReadFromUDP(result)
	if err != nil {
		t.Fatal(err)
	}
	if n < 10 || string(result[10:n]) != "real-xray-udp" {
		t.Fatalf("UDP response did not cross real VLESS: %v", result[:n])
	}
}
