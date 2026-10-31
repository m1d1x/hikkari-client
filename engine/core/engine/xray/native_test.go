package xray

import (
	"context"
	"encoding/json"
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

	"NekkoVPN/core/engine"
	"NekkoVPN/core/profile"
	"NekkoVPN/core/routing"
	"golang.org/x/net/proxy"
)

func TestNativeVPNConfig(t *testing.T) {
	bin := engineBinary(t)
	ps, err := Decode("vless://11111111-2222-3333-4444-555555555555@127.0.0.1:443#Native")
	if err != nil {
		t.Fatal(err)
	}
	ps[0].Name = "server"
	ports, err := AllocatePorts(ps, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, auto := range []bool{false, true} {
		c := engine.Configuration{Profiles: ps, ActiveProfile: "server", AutoSelect: auto, Routing: routing.DefaultRoutingState()}
		c.Routing.Bypass.Domains = []string{"example.test"}
		c.Routing.Bypass.IPs = []string{"192.0.2.0/24"}
		cfg, err := CompileVPN(c, ports)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "xray.json")
		writeJSON(t, path, cfg)
		if output, err := exec.Command(bin, "run", "-test", "-c", path).CombinedOutput(); err != nil {
			t.Fatalf("native TUN configuration rejected: %s", output)
		}
	}
}

// This host uses the real production compiler and Xray binary. Only native
// TUN creation is replaced with a local SOCKS ingress under the same tag.
// No privileged commands, external engine or system DNS changes are allowed.
type localNativeHost struct {
	p       *Process
	ports   map[string]int
	ingress int
}

func (h *localNativeHost) Start(ctx context.Context) error {
	raw, err := os.ReadFile(h.p.Config)
	if err != nil {
		return err
	}
	var cfg object
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return err
	}
	inbounds := cfg["inbounds"].([]interface{})
	for _, item := range inbounds {
		in := item.(map[string]interface{})
		if in["protocol"] == "tun" {
			in["protocol"] = "socks"
			in["listen"] = "127.0.0.1"
			in["port"] = h.ingress
			in["settings"] = object{"auth": "noauth", "udp": true, "ip": "127.0.0.1"}
		}
	}
	if obs, ok := cfg["observatory"].(map[string]interface{}); ok {
		obs["probeURL"] = "http://127.0.0.1:12345/probe"
		obs["probeInterval"] = "1s"
	}
	write, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := os.WriteFile(h.p.Config, write, 0600); err != nil {
		return err
	}
	return h.p.Start(ctx, h.ports)
}
func (h *localNativeHost) Stop() error     { return h.p.Stop() }
func (h *localNativeHost) IsRunning() bool { return h.p.IsRunning() }
func availablePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestStandaloneXrayPolicySelectionAndProbes(t *testing.T) {
	bin := engineBinary(t)
	dir := t.TempDir()
	ingress := availablePort(t)
	value, err := New(dir, nil, func(binary, path string, ports map[string]int) Host {
		return &localNativeHost{p: &Process{Binary: binary, Config: path, Directory: dir}, ports: ports, ingress: ingress}
	})
	if err != nil {
		t.Fatal(err)
	}
	b := value.(*Backend)
	defer b.Stop()
	// ResolveBinary must see Xray only; this directory contains no sing-box.
	if err := os.Symlink(bin, filepath.Join(dir, "xray")); err != nil {
		t.Fatal(err)
	}
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "direct") }))
	defer direct.Close()
	var profiles []profile.Profile
	var servers []*exec.Cmd
	for _, name := range []string{"server", "p-server"} {
		marker := name
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(20 * time.Millisecond)
			io.WriteString(w, marker)
		}))
		defer target.Close()
		endpoint := availablePort(t)
		cfg := object{"log": object{"loglevel": "error"}, "inbounds": []interface{}{object{"listen": "127.0.0.1", "port": endpoint, "protocol": "vless", "settings": object{"decryption": "none", "clients": []interface{}{object{"id": "11111111-2222-3333-4444-555555555555"}}}}}, "outbounds": []interface{}{object{"protocol": "freedom", "settings": object{"redirect": target.Listener.Addr().String(), "finalRules": []interface{}{object{"action": "allow", "ip": []string{"127.0.0.1/32"}}}}}}}
		path := filepath.Join(dir, name+".json")
		writeJSON(t, path, cfg)
		server := exec.Command(bin, "run", "-c", path)
		if err := server.Start(); err != nil {
			t.Fatal(err)
		}
		servers = append(servers, server)
		defer func() { _ = server.Process.Kill(); _ = server.Wait() }()
		ps, err := Decode("vless://11111111-2222-3333-4444-555555555555@" + fmtAddress(endpoint) + "#" + name)
		if err != nil {
			t.Fatal(err)
		}
		ps[0].Name = name
		profiles = append(profiles, ps[0])
	}
	c := engine.Configuration{Profiles: profiles, ActiveProfile: "server", Routing: routing.DefaultRoutingState()}
	c.Routing.Bypass.Domains = []string{"bypass.test"}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	start := func() {
		t.Helper()
		if err := b.Configure(c); err != nil {
			t.Fatal(err)
		}
		if err := b.Start(ctx); err != nil {
			t.Fatal(err)
		}
	}
	restart := func() {
		t.Helper()
		if err := b.Stop(); err != nil {
			t.Fatal(err)
		}
		start()
	}
	start()
	dialer, err := proxy.SOCKS5("tcp", fmtAddress(ingress), nil, proxy.Direct)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{Dial: dialer.Dial, DisableKeepAlives: true}, Timeout: 3 * time.Second}
	defer client.CloseIdleConnections()
	fetch := func(host, want string) {
		t.Helper()
		req, _ := http.NewRequest("GET", direct.URL, nil)
		req.Host = host
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || string(raw) != want {
			t.Fatalf("policy host=%s: wanted %s got %q err=%v", host, want, raw, err)
		}
	}
	fetch("other.test", "server")
	fetch("bypass.test", "direct")
	for _, p := range profiles {
		if _, err := b.Delay(p.Name, "http://127.0.0.1:12345/probe", 2000); err != nil {
			t.Fatal(err)
		}
	}
	c.ActiveProfile = "p-server"
	restart()
	fetch("other.test", "p-server")
	c.Routing.Mode = routing.ModeProxy
	c.Routing.Proxy.Domains = []string{"proxy.test"}
	restart()
	fetch("proxy.test", "p-server")
	fetch("other.test", "direct")
	c.Routing.Mode = routing.ModeBypass
	c.Routing.Bypass.Domains = nil
	c.Routing.Bypass.IPs = []string{"127.0.0.1/32"}
	restart()
	fetch("other.test", "direct")
	c.Routing.Enabled = false
	restart()
	fetch("other.test", "p-server")
	// Automatic selection must reject a dead candidate without another engine.
	_ = servers[0].Process.Kill()
	_ = servers[0].Wait()
	c.AutoSelect = true
	restart()
	var chosen string
	for end := time.Now().Add(12 * time.Second); time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
		resp, e := client.Get(direct.URL)
		if e != nil {
			continue
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		chosen = string(raw)
		if chosen == "p-server" {
			break
		}
	}
	if chosen != "p-server" {
		t.Fatalf("native Xray failover failed: %q", chosen)
	}
	if !b.IsRunning() {
		t.Fatal("standalone process not live")
	}
	if _, err := os.Stat(filepath.Join(dir, "config.json")); !os.IsNotExist(err) {
		t.Fatal("Xray mode generated another engine's configuration")
	}
	if err := b.Stop(); err != nil {
		t.Fatal(err)
	}
	if b.IsRunning() {
		t.Fatal("process survived stop")
	}
	t.Log("Xray only: shared domain/IP policy, bypass/proxy modes, profile selection, probes, native auto-selection and lifecycle pass")
}

func TestXrayDoesNotImportOtherAdapters(t *testing.T) {
	output, err := exec.Command("go", "list", "-deps", "NekkoVPN/core/engine/xray").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(output), "core/engine/singbox") || strings.Contains(string(output), "xraysingbox") {
		t.Fatal("cross-adapter dependency returned")
	}
}

func TestPrepareNativeTUNFixture(t *testing.T) {
	output := os.Getenv("HIKKARI_TUN_FIXTURE")
	if output == "" {
		t.Skip("fixture export is explicit")
	}
	profiles, err := Decode("vless://11111111-2222-3333-4444-555555555555@127.0.0.1:443#Native-TUN")
	if err != nil {
		t.Fatal(err)
	}
	profiles[0].Name = "fixture"
	config := engine.Configuration{Profiles: profiles, ActiveProfile: "fixture", Routing: routing.DefaultRoutingState()}
	config.Routing.Bypass.Processes = []string{"curl"}
	native, err := CompileVPN(config, map[string]int{"fixture": 12345})
	if err != nil {
		t.Fatal(err)
	}
	writeJSON(t, output, native)
}
