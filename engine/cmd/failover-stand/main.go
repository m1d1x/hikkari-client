// Command failover-stand measures how auto mode behaves when a server fails. It
// is a measuring tool, not a simulation of the product: the client is the real
// core running the config the engine generates from real profile links, and the
// servers are real core processes. Only the network between them is staged, by a
// proxy in front of each server that can blackhole, reset, delay or drop.
//
//	go run ./cmd/failover-stand -core /path/to/sing-box
//
// Everything stays on the loopback and needs no privileges. The TUN inbound and
// the fake-IP DNS are replaced by a local proxy inbound; route rules are dropped
// because the private-address bypass would send the loopback target direct.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"NekkoVPN/core/engine/singbox"
	configfile "NekkoVPN/core/engine/singbox/config"
)

const (
	standUUID  = "11111111-2222-3333-4444-555555555555"
	clashPort  = 21090
	clashAddr  = "http://127.0.0.1:21090"
	mixedPort  = 21080
	targetBase = 21100
)

type faultMode int32

const (
	pass faultMode = iota
	blackhole
	reset
	drop
)

// faultProxy sits between the client and one server: TCP for VLESS, UDP for
// Hysteria2. Modes apply to connections that already exist as well as new ones.
type faultProxy struct {
	addr    string
	backend string
	mode    atomic.Int32
	delayMs atomic.Int32

	mu    sync.Mutex
	conns map[net.Conn]bool
}

func (f *faultProxy) set(mode faultMode) {
	f.mode.Store(int32(mode))
	if mode != reset {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for conn := range f.conns {
		resetConn(conn)
	}
}

func resetConn(conn net.Conn) {
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetLinger(0)
	}
	_ = conn.Close()
}

func (f *faultProxy) serveTCP(ctx context.Context, listener net.Listener) {
	f.conns = map[net.Conn]bool{}
	go func() { <-ctx.Done(); _ = listener.Close() }()
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		switch faultMode(f.mode.Load()) {
		case reset:
			resetConn(conn)
			continue
		case blackhole:
			continue // accepted and never answered, held until the client gives up
		}
		upstream, err := net.Dial("tcp", f.backend)
		if err != nil {
			resetConn(conn)
			continue
		}
		f.mu.Lock()
		f.conns[conn] = true
		f.mu.Unlock()
		go f.pipe(conn, upstream)
		go f.pipe(upstream, conn)
	}
}

func (f *faultProxy) pipe(from, to net.Conn) {
	defer func() {
		_ = from.Close()
		_ = to.Close()
		f.mu.Lock()
		delete(f.conns, from)
		delete(f.conns, to)
		f.mu.Unlock()
	}()
	buffer := make([]byte, 32*1024)
	for {
		n, err := from.Read(buffer)
		if err != nil {
			return
		}
		// Bytes already in flight when the path dies never arrive.
		for faultMode(f.mode.Load()) == blackhole {
			time.Sleep(50 * time.Millisecond)
		}
		if d := f.delayMs.Load(); d > 0 {
			time.Sleep(time.Duration(d) * time.Millisecond)
		}
		if _, err := to.Write(buffer[:n]); err != nil {
			return
		}
	}
}

func (f *faultProxy) serveUDP(ctx context.Context, conn *net.UDPConn) {
	go func() { <-ctx.Done(); _ = conn.Close() }()
	backend, err := net.ResolveUDPAddr("udp", f.backend)
	if err != nil {
		log.Fatal(err)
	}
	var clients sync.Map
	buffer := make([]byte, 65535)
	for {
		n, client, err := conn.ReadFromUDP(buffer)
		if err != nil {
			return
		}
		if faultMode(f.mode.Load()) != pass {
			continue
		}
		key := client.String()
		value, known := clients.Load(key)
		if !known {
			upstream, err := net.DialUDP("udp", nil, backend)
			if err != nil {
				continue
			}
			clients.Store(key, upstream)
			value = upstream
			go func(addr *net.UDPAddr) {
				reply := make([]byte, 65535)
				for {
					n, err := upstream.Read(reply)
					if err != nil {
						return
					}
					if faultMode(f.mode.Load()) == pass {
						_, _ = conn.WriteToUDP(reply[:n], addr)
					}
				}
			}(client)
		}
		_, _ = value.(*net.UDPConn).Write(buffer[:n])
	}
}

// node is one server behind its fault proxy.
type node struct {
	name  string
	kind  string // vless-tls | hysteria2 | vless-xhttp
	proxy *faultProxy
	port  int
}

func (n *node) link() string {
	switch n.kind {
	case "hysteria2":
		return fmt.Sprintf("hysteria2://%s@127.0.0.1:%d?sni=stand.test&insecure=1#%s", standUUID, n.port, n.name)
	case "vless-xhttp":
		return fmt.Sprintf("vless://%s@127.0.0.1:%d?security=tls&sni=stand.test&allowInsecure=1&type=xhttp&path=%%2Fx&mode=packet-up&alpn=h2#%s", standUUID, n.port, n.name)
	}
	return fmt.Sprintf("vless://%s@127.0.0.1:%d?security=tls&sni=stand.test&allowInsecure=1&type=tcp#%s", standUUID, n.port, n.name)
}

type stand struct {
	core, xray, dir string
	ctx             context.Context
	procs           []*exec.Cmd
	nodes           []*node
	byPort          map[int]string
}

func (s *stand) spawn(name, binary string, args ...string) {
	cmd := exec.CommandContext(s.ctx, binary, args...)
	logFile, err := os.Create(filepath.Join(s.dir, name+".log"))
	if err != nil {
		log.Fatal(err)
	}
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		log.Fatalf("%s: %v", name, err)
	}
	s.procs = append(s.procs, cmd)
}

func writeJSON(path string, value interface{}) {
	raw, err := json.MarshalIndent(value, "", " ")
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		log.Fatal(err)
	}
}

// addNode starts the server and the proxy in front of it. index gives the node a
// fixed exit: its direct outbound always lands on its own target port, so the
// target can say which server a request left through.
func (s *stand) addNode(index int, name, kind string) *node {
	backendPort := 21200 + index
	frontPort := 21300 + index
	exit := targetBase + index
	backend := fmt.Sprintf("127.0.0.1:%d", backendPort)
	proxy := &faultProxy{addr: fmt.Sprintf("127.0.0.1:%d", frontPort), backend: backend}
	tls := map[string]interface{}{"enabled": true, "server_name": "stand.test",
		"certificate_path": filepath.Join(s.dir, "cert.pem"), "key_path": filepath.Join(s.dir, "key.pem")}
	route := map[string]interface{}{"rules": []interface{}{
		map[string]interface{}{"inbound": []interface{}{"in"}, "action": "route-options", "override_address": "127.0.0.1", "override_port": exit},
		map[string]interface{}{"inbound": []interface{}{"in"}, "outbound": "out"},
	}, "final": "out"}
	outbounds := []interface{}{map[string]interface{}{"type": "direct", "tag": "out"}}
	switch kind {
	case "hysteria2":
		tls["alpn"] = []interface{}{"h3"}
		config := filepath.Join(s.dir, name+".json")
		writeJSON(config, map[string]interface{}{"log": map[string]interface{}{"level": "warn"}, "inbounds": []interface{}{map[string]interface{}{
			"type": "hysteria2", "tag": "in", "listen": "127.0.0.1", "listen_port": backendPort,
			"users": []interface{}{map[string]interface{}{"password": standUUID}}, "tls": tls}}, "outbounds": outbounds, "route": route})
		s.spawn(name, s.core, "run", "-c", config)
		conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: frontPort})
		if err != nil {
			log.Fatal(err)
		}
		go proxy.serveUDP(s.ctx, conn)
	case "vless-xhttp":
		if s.xray == "" {
			log.Fatal("vless-xhttp needs -xray")
		}
		config := filepath.Join(s.dir, name+".json")
		writeJSON(config, map[string]interface{}{"log": map[string]interface{}{"loglevel": "warning"},
			"inbounds": []interface{}{map[string]interface{}{"listen": "127.0.0.1", "port": backendPort, "protocol": "vless",
				"settings": map[string]interface{}{"clients": []interface{}{map[string]interface{}{"id": standUUID}}, "decryption": "none"},
				"streamSettings": map[string]interface{}{"network": "xhttp", "security": "tls",
					"tlsSettings":   map[string]interface{}{"alpn": []interface{}{"h2"}, "certificates": []interface{}{map[string]interface{}{"certificateFile": tls["certificate_path"], "keyFile": tls["key_path"]}}},
					"xhttpSettings": map[string]interface{}{"path": "/x", "mode": "packet-up"}}}},
			"outbounds": []interface{}{map[string]interface{}{"protocol": "freedom", "settings": map[string]interface{}{"redirect": fmt.Sprintf("127.0.0.1:%d", exit), "finalRules": []interface{}{map[string]interface{}{"action": "allow", "ip": []string{"127.0.0.1/32"}}}}}}})
		s.spawn(name, s.xray, "run", "-c", config)
		s.listenTCP(proxy, frontPort)
	default:
		config := filepath.Join(s.dir, name+".json")
		writeJSON(config, map[string]interface{}{"log": map[string]interface{}{"level": "warn"}, "inbounds": []interface{}{map[string]interface{}{
			"type": "vless", "tag": "in", "listen": "127.0.0.1", "listen_port": backendPort,
			"users": []interface{}{map[string]interface{}{"uuid": standUUID}}, "tls": tls}}, "outbounds": outbounds, "route": route})
		s.spawn(name, s.core, "run", "-c", config)
		s.listenTCP(proxy, frontPort)
	}
	s.serveTarget(exit, name)
	n := &node{name: name, kind: kind, proxy: proxy, port: frontPort}
	s.nodes = append(s.nodes, n)
	return n
}

func (s *stand) listenTCP(proxy *faultProxy, port int) {
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		log.Fatal(err)
	}
	go proxy.serveTCP(s.ctx, listener)
}

// serveTarget answers with the node's name, the stand's version of an exit IP.
// /stream sends a byte every 200ms, for as long as the client stays connected.
func (s *stand) serveTarget(port int, name string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/generate_204", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("/exit", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, name) })
	mux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		for r.Context().Err() == nil {
			_, _ = w.Write([]byte("x"))
			flusher.Flush()
			time.Sleep(200 * time.Millisecond)
		}
	})
	server := &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", port), Handler: mux}
	go func() { <-s.ctx.Done(); _ = server.Close() }()
	go func() { _ = server.ListenAndServe() }()
}

// startClient builds the client exactly the way the engine does, from links, and
// swaps only what the loopback requires.
func (s *stand) startClient() {
	path := filepath.Join(s.dir, "client.json")
	store := configfile.New(path)
	var specs []configfile.ProfileSpec
	for _, n := range s.nodes {
		draft, err := singbox.ParseURI(n.link())
		if err != nil {
			log.Fatalf("%s: %v", n.name, err)
		}
		draft.Config["tag"] = "p-" + n.name
		specs = append(specs, configfile.ProfileSpec{Tag: "p-" + n.name, Kind: "outbound", Config: draft.Config})
	}
	if err := store.RebuildActiveWithRouting(specs, specs[0].Tag, true, nil, configfile.RoutingState{}, configfile.HostNetwork{}); err != nil {
		log.Fatal(err)
	}
	cfg, err := store.Read()
	if err != nil {
		log.Fatal(err)
	}
	for _, raw := range cfg["outbounds"].([]interface{}) {
		if group := raw.(map[string]interface{}); group["tag"] == configfile.AutoTag {
			group["url"] = fmt.Sprintf("http://127.0.0.1:%d/generate_204", targetBase)
		}
	}
	cfg["inbounds"] = []interface{}{map[string]interface{}{"type": "mixed", "tag": "mixed-in", "listen": "127.0.0.1", "listen_port": mixedPort}}
	cfg["dns"] = map[string]interface{}{"servers": []interface{}{map[string]interface{}{"tag": "dns-direct", "type": "local"}}}
	cfg["route"] = map[string]interface{}{"final": configfile.AutoTag, "default_domain_resolver": map[string]interface{}{"server": "dns-direct"}}
	cfg["log"] = map[string]interface{}{"level": "debug", "timestamp": true}
	exp := cfg["experimental"].(map[string]interface{})
	delete(exp, "cache_file")
	// Off the product's port, so a tunnel the user has up is not disturbed.
	exp["clash_api"] = map[string]interface{}{"external_controller": fmt.Sprintf("127.0.0.1:%d", clashPort)}
	writeJSON(path, cfg)
	s.spawn("client", s.core, "run", "-c", path)
}

func clashNow() string {
	resp, err := http.Get(clashAddr + "/proxies/" + configfile.AutoTag)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var out struct {
		Now string `json:"now"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return strings.TrimPrefix(out.Now, "p-")
}

func proxyClient(timeout time.Duration) *http.Client {
	proxy, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", mixedPort))
	return &http.Client{Timeout: timeout, Transport: &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}}
}

// exitOf makes one new connection and returns the node it left through.
func exitOf(timeout time.Duration) (string, error) {
	resp, err := proxyClient(timeout).Get(fmt.Sprintf("http://127.0.0.1:%d/exit", targetBase))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("http %d", resp.StatusCode)
	}
	return string(body), err
}

// stream is one long connection, standing in for a call or a download.
type stream struct {
	exit     atomic.Value
	lastByte atomic.Int64
	broken   atomic.Value
	cancel   context.CancelFunc
}

func openStream(ctx context.Context) *stream {
	ctx, cancel := context.WithCancel(ctx)
	st := &stream{cancel: cancel}
	st.lastByte.Store(time.Now().UnixNano())
	go func() {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/stream", targetBase), nil)
		resp, err := proxyClient(0).Do(request)
		if err != nil {
			st.broken.Store(err.Error())
			return
		}
		defer resp.Body.Close()
		buffer := make([]byte, 64)
		for {
			if _, err := resp.Body.Read(buffer); err != nil {
				if ctx.Err() == nil {
					st.broken.Store(err.Error())
				}
				return
			}
			st.lastByte.Store(time.Now().UnixNano())
		}
	}()
	return st
}

func (s *stream) alive() (bool, string) {
	if reason := s.broken.Load(); reason != nil {
		return false, reason.(string)
	}
	if idle := time.Since(time.Unix(0, s.lastByte.Load())); idle > 3*time.Second {
		return false, fmt.Sprintf("no bytes for %s", idle.Round(time.Second))
	}
	return true, ""
}

type result struct {
	Scenario string `json:"scenario"`
	Pass     bool   `json:"pass"`
	Detail   string `json:"detail"`
}

// waitHeld waits until the group settled on a node and new connections use it.
func waitHeld(timeout time.Duration) *string {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if now := clashNow(); now != "" {
			if exit, err := exitOf(5 * time.Second); err == nil && exit == now {
				return &exit
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return nil
}

func (s *stand) node(name string) *node {
	for _, n := range s.nodes {
		if n.name == name {
			return n
		}
	}
	log.Fatalf("no node %q", name)
	return nil
}

// measureFailover applies a fault to the held node while new connections keep
// arriving every 500ms, as browsing would, and times the first one that succeeds
// through another node. The moving user traffic is what the core reacts to.
func (s *stand) measureFailover(ctx context.Context, title string, mode faultMode, limit time.Duration) result {
	held := waitHeld(30 * time.Second)
	if held == nil {
		return result{title, false, "group never settled"}
	}
	victim := s.node(*held)
	bystander := openStream(ctx)
	defer bystander.cancel()
	time.Sleep(time.Second)
	start := time.Now()
	victim.proxy.set(mode)
	defer victim.proxy.set(pass)

	type outcome struct {
		exit string
		at   time.Duration
	}
	done := make(chan outcome, 1)
	var failed atomic.Int32
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				go func() {
					exit, err := exitOf(20 * time.Second)
					if err != nil {
						failed.Add(1)
						return
					}
					if exit != victim.name {
						select {
						case done <- outcome{exit, time.Since(start)}:
						default:
						}
					}
				}()
			}
		}
	}()
	select {
	case first := <-done:
		// Requests still parked on the dead node have not failed yet: "failed" counts
		// only those that already gave up, not everything sent before the move.
		detail := fmt.Sprintf("%s failed: first request through %s after %s, %d requests already failed, group holds %q",
			victim.name, first.exit, first.at.Round(10*time.Millisecond), failed.Load(), clashNow())
		return result{title, first.at <= limit, detail}
	case <-time.After(limit + 60*time.Second):
		return result{title, false, fmt.Sprintf("%s failed: no other node within %s, %d requests failed", victim.name, limit+60*time.Second, failed.Load())}
	}
}

// measureJitter runs every node at a 150ms RTT and swings the held one by up to
// +75ms for two probe ticks: neither the exit nor a live connection may change.
// On the bare loopback the same swing would be a 50x slowdown, a real degradation
// the group is right to leave.
func (s *stand) measureJitter(ctx context.Context, duration time.Duration) result {
	const title = "jitter +-75ms"
	const oneWayMs = 75
	for _, n := range s.nodes {
		n.proxy.delayMs.Store(oneWayMs)
		defer n.proxy.delayMs.Store(0)
	}
	// Let the group measure everyone at the new baseline.
	time.Sleep(90 * time.Second)
	held := waitHeld(30 * time.Second)
	if held == nil {
		return result{title, false, "group never settled"}
	}
	victim := s.node(*held)
	live := openStream(ctx)
	defer live.cancel()
	time.Sleep(time.Second)
	end := time.Now().Add(duration)
	moves := 0
	for time.Now().Before(end) {
		victim.proxy.delayMs.Store(oneWayMs + int32(time.Now().UnixNano()%38))
		if now := clashNow(); now != "" && now != *held {
			moves++
		}
		time.Sleep(2 * time.Second)
	}
	alive, reason := live.alive()
	detail := fmt.Sprintf("held %s for %s, %d readings elsewhere, live connection alive=%v %s", *held, duration, moves, alive, reason)
	return result{title, moves == 0 && alive, detail}
}

// measureDegrade makes the held node far slower than its neighbours. New
// connections should move; the one already open stays on the node it started on.
func (s *stand) measureDegrade(ctx context.Context, tick time.Duration) result {
	const title = "degraded held node"
	held := waitHeld(30 * time.Second)
	if held == nil {
		return result{title, false, "group never settled"}
	}
	victim := s.node(*held)
	defer victim.proxy.delayMs.Store(0)
	live := openStream(ctx)
	defer live.cancel()
	time.Sleep(time.Second)
	start := time.Now()
	victim.proxy.delayMs.Store(250)
	for time.Since(start) < 3*tick {
		if now := clashNow(); now != "" && now != *held {
			alive, reason := live.alive()
			detail := fmt.Sprintf("moved %s -> %s after %s, open connection alive=%v %s", *held, now, time.Since(start).Round(time.Second), alive, reason)
			return result{title, alive, detail}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return result{title, false, fmt.Sprintf("still on %s after %s", *held, 3*tick)}
}

func writeCert(dir string) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		log.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "stand.test"},
		DNSNames: []string{"stand.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		log.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		log.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "cert.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "key.pem"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600)
}

func main() {
	core := flag.String("core", "", "path to the sing-box binary under test")
	xray := flag.String("xray", "", "path to xray, for the XHTTP server (optional)")
	only := flag.String("only", "", "comma-separated scenarios: blackhole,rst,udp,jitter,degrade,xhttp-blackhole,xhttp-rst")
	out := flag.String("out", "", "write the results as JSON")
	flag.Parse()
	if *core == "" {
		log.Fatal("-core is required")
	}
	const tick = 120 * time.Second // autoProbeInterval in the generated config

	want := func(name string) bool {
		if *only == "" {
			return name != "xhttp-blackhole" && name != "xhttp-rst" || *xray != ""
		}
		return strings.Contains(","+*only+",", ","+name+",")
	}

	run := func(kinds [][2]string, body func(ctx context.Context, s *stand) []result) []result {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		dir, err := os.MkdirTemp("", "failover-stand-")
		if err != nil {
			log.Fatal(err)
		}
		writeCert(dir)
		s := &stand{core: *core, xray: *xray, dir: dir, ctx: ctx}
		for i, kind := range kinds {
			s.addNode(i, kind[0], kind[1])
		}
		time.Sleep(time.Second)
		s.startClient()
		time.Sleep(2 * time.Second)
		results := body(ctx, s)
		cancel()
		for _, proc := range s.procs {
			_ = proc.Wait()
		}
		time.Sleep(500 * time.Millisecond)
		log.Printf("logs: %s", dir)
		return results
	}

	vless := [][2]string{{"a", "vless-tls"}, {"b", "vless-tls"}, {"c", "vless-tls"}}
	var results []result
	each := func(name string, kinds [][2]string, body func(ctx context.Context, s *stand) result) {
		if !want(name) {
			return
		}
		log.Printf("scenario %s", name)
		got := run(kinds, func(ctx context.Context, s *stand) []result { return []result{body(ctx, s)} })
		log.Printf("  %+v", got[0])
		results = append(results, got...)
	}
	each("blackhole", vless, func(ctx context.Context, s *stand) result {
		return s.measureFailover(ctx, "blackhole, vless+tls", blackhole, 8*time.Second)
	})
	each("rst", vless, func(ctx context.Context, s *stand) result {
		return s.measureFailover(ctx, "rst, vless+tls", reset, 2*time.Second)
	})
	each("udp", [][2]string{{"h", "hysteria2"}, {"h2", "hysteria2"}}, func(ctx context.Context, s *stand) result {
		return s.measureFailover(ctx, "udp drop, hysteria2", drop, 12*time.Second)
	})
	each("jitter", vless, func(ctx context.Context, s *stand) result { return s.measureJitter(ctx, 2*tick+15*time.Second) })
	each("degrade", vless, func(ctx context.Context, s *stand) result { return s.measureDegrade(ctx, tick) })
	xhttp := [][2]string{{"x1", "vless-xhttp"}, {"x2", "vless-xhttp"}}
	each("xhttp-blackhole", xhttp, func(ctx context.Context, s *stand) result {
		return s.measureFailover(ctx, "blackhole, vless+xhttp", blackhole, 12*time.Second)
	})
	each("xhttp-rst", xhttp, func(ctx context.Context, s *stand) result {
		return s.measureFailover(ctx, "rst, vless+xhttp", reset, 3*time.Second)
	})

	failed := 0
	for _, r := range results {
		mark := "PASS"
		if !r.Pass {
			mark = "FAIL"
			failed++
		}
		fmt.Printf("%s  %-26s %s\n", mark, r.Scenario, r.Detail)
	}
	if *out != "" {
		writeJSON(*out, results)
	}
	if failed > 0 {
		os.Exit(1)
	}
}
