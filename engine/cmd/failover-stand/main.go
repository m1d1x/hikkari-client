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
	"encoding/binary"
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
	"NekkoVPN/core/engine/singbox/clashapi"
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
// The same port over UDP is the voice server: it echoes every packet back after
// "exit:", the node's name and a newline. A STUN Binding Request gets a Binding Success
// instead, so the core's voice watchdog, which pings a public STUN server, finds
// one at every exit.
func (s *stand) serveTarget(port int, name string) {
	echo, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	if err != nil {
		log.Fatal(err)
	}
	go func() { <-s.ctx.Done(); _ = echo.Close() }()
	go func() {
		buffer := make([]byte, 2048)
		for {
			n, from, err := echo.ReadFromUDP(buffer)
			if err != nil {
				return
			}
			if n >= 20 && binary.BigEndian.Uint16(buffer) == 0x0001 && binary.BigEndian.Uint32(buffer[4:]) == 0x2112A442 {
				response := make([]byte, 20)
				binary.BigEndian.PutUint16(response, 0x0101)
				copy(response[4:], buffer[4:20])
				_, _ = echo.WriteToUDP(response, from)
				continue
			}
			_, _ = echo.WriteToUDP(append([]byte(echoPrefix+name+"\n"), buffer[:n]...), from)
		}
	}()

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

const (
	voiceInterval = 20 * time.Millisecond // 50 packets a second, an Opus call
	voiceSize     = 160
)

// voice is one UDP association through the client's SOCKS5 inbound, standing in
// for a call socket. Every packet carries a sequence number; the exit echoes it.
// Nothing else goes through the client while a voice scenario runs, so the core
// sees what it sees when only a call is up.
type voice struct {
	ctrl  net.Conn
	conn  *net.UDPConn
	relay *net.UDPAddr

	mu       sync.Mutex
	seq      uint64
	mark     time.Time // counting starts here; zero until markNow
	markSeq  uint64
	last     time.Time // last echo received
	maxGap   time.Duration
	sent     int64 // since mark
	received int64 // echoes of packets sent since mark
	exit     string
}

func dialVoice(timeout time.Duration) (*voice, error) {
	ctrl, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", mixedPort), timeout)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*voice, error) {
		_ = ctrl.Close()
		return nil, err
	}
	_ = ctrl.SetDeadline(time.Now().Add(timeout))
	if _, err := ctrl.Write([]byte{5, 1, 0}); err != nil {
		return fail(err)
	}
	greeting := make([]byte, 2)
	if _, err := io.ReadFull(ctrl, greeting); err != nil || greeting[1] != 0 {
		return fail(fmt.Errorf("socks greeting %v: %v", greeting, err))
	}
	if _, err := ctrl.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return fail(err)
	}
	head := make([]byte, 4)
	if _, err := io.ReadFull(ctrl, head); err != nil || head[1] != 0 {
		return fail(fmt.Errorf("socks udp associate %v: %v", head, err))
	}
	var ip net.IP
	switch head[3] {
	case 1:
		ip = make(net.IP, 4)
	case 4:
		ip = make(net.IP, 16)
	default:
		return fail(fmt.Errorf("socks bind address type %d", head[3]))
	}
	port := make([]byte, 2)
	if _, err := io.ReadFull(ctrl, ip); err != nil {
		return fail(err)
	}
	if _, err := io.ReadFull(ctrl, port); err != nil {
		return fail(err)
	}
	_ = ctrl.SetDeadline(time.Time{})
	if ip.IsUnspecified() {
		ip = net.IPv4(127, 0, 0, 1)
	}
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return fail(err)
	}
	return &voice{ctrl: ctrl, conn: conn, relay: &net.UDPAddr{IP: ip, Port: int(binary.BigEndian.Uint16(port))}}, nil
}

func (v *voice) close() {
	_ = v.ctrl.Close()
	_ = v.conn.Close()
}

func (v *voice) send() error {
	v.mu.Lock()
	v.seq++
	seq := v.seq
	if !v.mark.IsZero() {
		v.sent++
	}
	v.mu.Unlock()
	packet := make([]byte, 10+voiceSize)
	copy(packet, []byte{0, 0, 0, 1, 127, 0, 0, 1})
	binary.BigEndian.PutUint16(packet[8:], targetBase)
	binary.BigEndian.PutUint64(packet[10:], seq)
	_, err := v.conn.WriteToUDP(packet, v.relay)
	return err
}

const echoPrefix = "exit:"

// strayDatagrams counts relayed datagrams that are not echoes of ours: a reply
// delivered to the wrong flow.
var strayDatagrams atomic.Int64

// parseEcho reads a relayed echo: the SOCKS5 UDP header, the exit's name, then
// the packet that was sent.
func parseEcho(datagram []byte) (seq uint64, exit string, ok bool) {
	if len(datagram) < 4 {
		return 0, "", false
	}
	header := map[byte]int{1: 10, 4: 22}[datagram[3]]
	if header == 0 || len(datagram) < header {
		return 0, "", false
	}
	body, found := strings.CutPrefix(string(datagram[header:]), echoPrefix)
	newline := strings.IndexByte(body, '\n')
	if !found || newline < 0 || len(body) < newline+1+8 {
		if strayDatagrams.Add(1) == 1 {
			log.Printf("stray datagram on a call socket: %x", datagram[:min(len(datagram), 48)])
		}
		return 0, "", false
	}
	return binary.BigEndian.Uint64([]byte(body[newline+1:])), body[:newline], true
}

// call sends at the voice rate and records echoes until ctx ends.
func (v *voice) call(ctx context.Context) {
	go func() {
		buffer := make([]byte, 2048)
		for {
			n, err := v.conn.Read(buffer)
			if err != nil {
				return
			}
			seq, exit, ok := parseEcho(buffer[:n])
			if !ok {
				continue
			}
			now := time.Now()
			v.mu.Lock()
			if !v.mark.IsZero() {
				// The gap that spans the mark counts: it is the one the fault opened.
				if gap := now.Sub(v.last); gap > v.maxGap {
					v.maxGap = gap
				}
				if seq > v.markSeq {
					v.received++
				}
			}
			v.last, v.exit = now, exit
			v.mu.Unlock()
		}
	}()
	ticker := time.NewTicker(voiceInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = v.send()
		}
	}
}

func (v *voice) markNow() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.mark, v.markSeq = time.Now(), v.seq
	v.sent, v.received, v.maxGap = 0, 0, 0
}

type callStats struct {
	maxGap time.Duration // includes the silence still open now
	loss   float64
	exit   string
	silent time.Duration
}

func (v *voice) stats() callStats {
	v.mu.Lock()
	defer v.mu.Unlock()
	st := callStats{maxGap: v.maxGap, exit: v.exit, silent: time.Since(v.last)}
	if st.silent > st.maxGap {
		st.maxGap = st.silent
	}
	if v.sent > 0 {
		st.loss = 1 - float64(v.received)/float64(v.sent)
	}
	return st
}

func (st callStats) String() string {
	return fmt.Sprintf("longest gap %s, lost %.0f%%, last echo from %s %s ago",
		st.maxGap.Round(10*time.Millisecond), st.loss*100, st.exit, st.silent.Round(10*time.Millisecond))
}

// udpExitOf opens a new call socket, as Discord does every minute, and returns
// the node its first echo came through.
func udpExitOf(timeout time.Duration) (string, error) {
	v, err := dialVoice(timeout)
	if err != nil {
		return "", err
	}
	defer v.close()
	got := make(chan string, 1)
	go func() {
		buffer := make([]byte, 2048)
		for {
			n, err := v.conn.Read(buffer)
			if err != nil {
				return
			}
			if _, exit, ok := parseEcho(buffer[:n]); ok {
				got <- exit
				return
			}
		}
	}()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.After(timeout)
	for {
		_ = v.send()
		select {
		case exit := <-got:
			return exit, nil
		case <-ticker.C:
		case <-deadline:
			return "", fmt.Errorf("no echo within %s", timeout)
		}
	}
}

// startCall opens the call that runs through the whole scenario and waits until
// it echoes through the held node.
func startCall(ctx context.Context, held string) (*voice, error) {
	v, err := dialVoice(5 * time.Second)
	if err != nil {
		return nil, err
	}
	go v.call(ctx)
	time.Sleep(2 * time.Second)
	if st := v.stats(); st.exit != held || st.silent > time.Second {
		v.close()
		return nil, fmt.Errorf("call not flowing through %s: %s", held, st)
	}
	return v, nil
}

// measureVoiceFailover applies a fault to the held node while only a call is up.
// A new call socket opens every 100ms, and the first one echoed through another
// node is the time a client that recreates its socket gets its voice back. The
// call opened before the fault is reported separately: its flow stays where it
// started. limit is the target; the scenario waits up to wait for the move.
func (s *stand) measureVoiceFailover(ctx context.Context, title string, mode faultMode, limit, wait time.Duration) result {
	held := waitHeld(30 * time.Second)
	if held == nil {
		return result{title, false, "group never settled"}
	}
	victim := s.node(*held)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	old, err := startCall(ctx, *held)
	if err != nil {
		return result{title, false, err.Error()}
	}
	defer old.close()
	start := time.Now()
	old.markNow()
	victim.proxy.set(mode)
	defer victim.proxy.set(pass)

	type outcome struct {
		exit string
		at   time.Duration
	}
	done := make(chan outcome, 1)
	var failed atomic.Int32
	go func() {
		// Every 100ms: a move measured in seconds needs a finer step than browsing's 500ms.
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				go func() {
					exit, err := udpExitOf(3 * time.Second)
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
		// Give the old call time to show whether it ever comes back.
		time.Sleep(10 * time.Second)
		detail := fmt.Sprintf("%s failed: new socket through %s after %s, %d new sockets got no echo, group holds %q; call opened before: %s",
			victim.name, first.exit, first.at.Round(10*time.Millisecond), failed.Load(), clashNow(), old.stats())
		return result{title, first.at <= limit, detail}
	case <-time.After(wait):
		return result{title, false, fmt.Sprintf("%s failed: no new socket through another node within %s, %d got no echo; call opened before: %s",
			victim.name, wait, failed.Load(), old.stats())}
	}
}

// measureVoiceAllDown takes every node down at once for outage, as a dying
// Wi-Fi would, then brings them back. Nothing is better to move to: the group
// must stay put and the call must resume on the node it had.
func (s *stand) measureVoiceAllDown(ctx context.Context, outage time.Duration) result {
	const title = "voice, all nodes down"
	held := waitHeld(30 * time.Second)
	if held == nil {
		return result{title, false, "group never settled"}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	call, err := startCall(ctx, *held)
	if err != nil {
		return result{title, false, err.Error()}
	}
	defer call.close()
	call.markNow()
	for _, n := range s.nodes {
		n.proxy.set(blackhole)
	}
	moves := 0
	end := time.Now().Add(outage + 30*time.Second)
	restored := false
	for start := time.Now(); time.Now().Before(end); time.Sleep(500 * time.Millisecond) {
		if !restored && time.Since(start) >= outage {
			for _, n := range s.nodes {
				n.proxy.set(pass)
			}
			restored = true
		}
		if now := clashNow(); now != "" && now != *held {
			moves++
		}
	}
	st := call.stats()
	resumed := st.silent < time.Second
	detail := fmt.Sprintf("held %s, all down for %s, %d readings elsewhere, call resumed=%v: %s", *held, outage, moves, resumed, st)
	return result{title, moves == 0 && resumed, detail}
}

// measureVoiceJitter is measureJitter with only a call up: the swing on the held
// node must not move the group or break the call.
func (s *stand) measureVoiceJitter(ctx context.Context, duration time.Duration) result {
	const title = "voice, jitter +-75ms"
	const oneWayMs = 75
	for _, n := range s.nodes {
		n.proxy.delayMs.Store(oneWayMs)
		defer n.proxy.delayMs.Store(0)
	}
	time.Sleep(90 * time.Second)
	held := waitHeld(30 * time.Second)
	if held == nil {
		return result{title, false, "group never settled"}
	}
	victim := s.node(*held)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	call, err := startCall(ctx, *held)
	if err != nil {
		return result{title, false, err.Error()}
	}
	defer call.close()
	call.markNow()
	end := time.Now().Add(duration)
	moves := 0
	for time.Now().Before(end) {
		victim.proxy.delayMs.Store(oneWayMs + int32(time.Now().UnixNano()%38))
		if now := clashNow(); now != "" && now != *held {
			moves++
		}
		time.Sleep(2 * time.Second)
	}
	st := call.stats()
	detail := fmt.Sprintf("held %s for %s, %d readings elsewhere, call: %s", *held, duration, moves, st)
	return result{title, moves == 0 && st.silent < time.Second, detail}
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
	only := flag.String("only", "", "comma-separated scenarios: capacity,blackhole,rst,udp,jitter,degrade,xhttp-blackhole,xhttp-rst,"+
		"voice-blackhole,voice-rst,voice-udp,voice-all-down,voice-jitter")
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
	each("capacity", vless, func(ctx context.Context, s *stand) result { return s.measureCapacityAdvice(ctx) })
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
	// Only a call is up: the target is a new socket on a working node within 2s.
	// The wait covers a planned probe tick, which is all the core has today.
	voiceWait := tick + 60*time.Second
	each("voice-blackhole", vless, func(ctx context.Context, s *stand) result {
		return s.measureVoiceFailover(ctx, "voice, blackhole, vless+tls", blackhole, 2*time.Second, voiceWait)
	})
	each("voice-rst", vless, func(ctx context.Context, s *stand) result {
		return s.measureVoiceFailover(ctx, "voice, rst, vless+tls", reset, 2*time.Second, voiceWait)
	})
	each("voice-udp", [][2]string{{"h", "hysteria2"}, {"h2", "hysteria2"}}, func(ctx context.Context, s *stand) result {
		return s.measureVoiceFailover(ctx, "voice, udp drop, hysteria2", drop, 2*time.Second, voiceWait)
	})
	each("voice-all-down", vless, func(ctx context.Context, s *stand) result { return s.measureVoiceAllDown(ctx, 10*time.Second) })
	each("voice-jitter", vless, func(ctx context.Context, s *stand) result { return s.measureVoiceJitter(ctx, 2*tick+15*time.Second) })

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

func (s *stand) measureCapacityAdvice(ctx context.Context) result {
	const title = "hub capacity advice, live core"
	held := waitHeld(30 * time.Second)
	if held == nil {
		return result{title, false, "no initial selection"}
	}
	preferred := ""
	for _, n := range s.nodes {
		if n.name != *held {
			preferred = n.name
			break
		}
	}
	live := openStream(ctx)
	defer live.cancel()
	time.Sleep(500 * time.Millisecond)
	control := clashapi.New(clashAddr)
	supported, err := control.SetPreference([]string{"p-" + preferred}, time.Now().Add(time.Minute).Unix())
	if err != nil || !supported {
		return result{title, false, fmt.Sprintf("advice API: %v supported=%v", err, supported)}
	}
	for until := time.Now().Add(5 * time.Second); time.Now().Before(until) && clashNow() != preferred; time.Sleep(100 * time.Millisecond) {
	}
	if clashNow() != preferred {
		return result{title, false, "new connections ignored initial capacity preference"}
	}
	// Later hub rankings must not move a healthy selection or break the old stream.
	_, err = control.SetPreference([]string{"p-" + *held}, time.Now().Add(time.Minute).Unix())
	time.Sleep(500 * time.Millisecond)
	alive, reason := live.alive()
	return result{title, err == nil && clashNow() == preferred && alive,
		fmt.Sprintf("initial %s -> preferred %s, later advice held=%v, established stream alive=%v %s", *held, preferred, clashNow() == preferred, alive, reason)}
}
