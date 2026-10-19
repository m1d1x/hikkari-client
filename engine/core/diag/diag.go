// Package diag dumps everything we might need to debug a user's machine
// remotely into the engine log: OS/network state, the effective sing-box
// config (secrets redacted), live connections with their process and
// outbound, and UDP/STUN probes that tell whether WebRTC-style traffic
// actually goes through the tunnel.
//
// Every line is prefixed with "[diag:<section>]" so a pasted log can be
// grepped. Nothing here changes system state — it only reads.
package diag

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// Logger is the slice of core/logging we need.
type Logger interface {
	Logf(format string, args ...interface{})
}

const clashBase = "http://127.0.0.1:9090"

// stunTargets are public STUN servers. The binding response carries the
// public address the server saw us from — the key signal for "did this UDP
// packet leave through the VPN or through the ISP directly".
var stunTargets = []string{
	"stun.l.google.com:19302",
	"stun.cloudflare.com:3478",
}

// Runner owns one diagnostic session per VPN start.
type Runner struct {
	log        Logger
	configPath string

	mu       sync.Mutex
	cancel   context.CancelFunc
	stunAddr []string // resolved before the TUN comes up (fake-ip afterwards)
	ifaces   []physIface
}

type physIface struct {
	Name string
	IP   net.IP
}

// New builds a Runner. configPath is the sing-box config.json.
func New(log Logger, configPath string) *Runner {
	return &Runner{log: log, configPath: configPath}
}

// Baseline snapshots the machine while the tunnel is down (call at engine
// init, in a goroutine — the Windows commands take ~10–20s in total):
// system info, interfaces, routes, other VPN software, direct STUN.
func (r *Runner) Baseline() {
	r.log.Logf("[diag] ===== baseline snapshot (tunnel down) =====")
	r.sysInfo()
	r.resolveStun()
	r.goInterfaces("pre")
	r.stunRound("direct/no-vpn")
	runCommands(r.log, "pre", preCommands())
	r.log.Logf("[diag] ===== baseline done =====")
}

// AfterStart logs the effective config and launches the background loop:
// post-start snapshot, then periodic probes + connection dumps for 10
// minutes. Cancelled by Stop or the next AfterStart.
func (r *Runner) AfterStart(parent context.Context) {
	r.dumpConfig()
	r.mu.Lock()
	if r.cancel != nil {
		r.cancel()
	}
	ctx, cancel := context.WithCancel(parent)
	r.cancel = cancel
	r.mu.Unlock()
	go r.loop(ctx)
}

// Stop ends the background loop (VPN stopped).
func (r *Runner) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
}

func (r *Runner) loop(ctx context.Context) {
	if !sleepCtx(ctx, 5*time.Second) {
		return
	}
	r.log.Logf("[diag] ===== post-start snapshot =====")
	r.mu.Lock()
	r.ifaces = physicalIfaces()
	r.mu.Unlock()
	r.clashInfo()
	r.goInterfaces("post")
	runCommands(r.log, "post", postCommands())
	r.probeRound(ctx)

	deadline := time.Now().Add(10 * time.Minute)
	tick := 0
	for time.Now().Before(deadline) {
		if !sleepCtx(ctx, 20*time.Second) {
			return
		}
		tick++
		r.log.Logf("[diag] ----- periodic #%d -----", tick)
		r.connections()
		runCommands(r.log, "periodic", periodicCommands())
		if tick%3 == 0 {
			r.probeRound(ctx)
		}
	}
	r.log.Logf("[diag] periodic diagnostics finished (10 min)")
}

func (r *Runner) probeRound(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	r.stunRound("tun/default-route")
	// WebRTC binds sockets to each physical NIC address. If these probes
	// succeed and report the ISP IP, voice traffic is leaking past the TUN.
	r.mu.Lock()
	ifaces := r.ifaces
	r.mu.Unlock()
	for _, pi := range ifaces {
		r.stunBound(pi)
	}
	r.proxyDelay()
	r.connections()
}

// ---------------------------------------------------------------- system

func (r *Runner) sysInfo() {
	host, _ := os.Hostname()
	exe, _ := os.Executable()
	r.log.Logf("[diag:sys] goos=%s goarch=%s cpus=%d host=%s exe=%s go=%s",
		runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), host, exe, runtime.Version())
	r.log.Logf("[diag:sys] tz=%s now=%s", time.Now().Location(), time.Now().Format(time.RFC3339))
}

func (r *Runner) goInterfaces(stage string) {
	ifs, err := net.Interfaces()
	if err != nil {
		r.log.Logf("[diag:ifaces/%s] error: %v", stage, err)
		return
	}
	for _, it := range ifs {
		addrs, _ := it.Addrs()
		as := make([]string, 0, len(addrs))
		for _, a := range addrs {
			as = append(as, a.String())
		}
		r.log.Logf("[diag:ifaces/%s] #%d %q mtu=%d flags=%s mac=%s addrs=%s",
			stage, it.Index, it.Name, it.MTU, it.Flags, it.HardwareAddr, strings.Join(as, ","))
	}
}

// physicalIfaces returns up, non-loopback IPv4 addresses that are not ours
// (tun 172.19.0.0/30) and not fake-ip.
func physicalIfaces() []physIface {
	var out []physIface
	ifs, _ := net.Interfaces()
	for _, it := range ifs {
		if it.Flags&net.FlagUp == 0 || it.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := it.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.To4() == nil {
				continue
			}
			ip := ipn.IP.To4()
			if ip[0] == 172 && ip[1] == 19 || ip[0] == 198 && (ip[1] == 18 || ip[1] == 19) || ip[0] == 169 && ip[1] == 254 {
				continue
			}
			out = append(out, physIface{Name: it.Name, IP: ip})
		}
	}
	return out
}

// ---------------------------------------------------------------- config

var secretKeys = map[string]bool{
	"uuid": true, "password": true, "private_key": true, "pre_shared_key": true,
	"auth": true, "auth_str": true, "token": true, "public_key": false,
}

func (r *Runner) dumpConfig() {
	b, err := os.ReadFile(r.configPath)
	if err != nil {
		r.log.Logf("[diag:config] read %s: %v", r.configPath, err)
		return
	}
	var cfg interface{}
	if err := json.Unmarshal(b, &cfg); err != nil {
		r.log.Logf("[diag:config] parse: %v", err)
		return
	}
	redact(cfg)
	out, _ := json.Marshal(cfg)
	r.log.Logf("[diag:config] %s", out)
}

func redact(v interface{}) {
	switch t := v.(type) {
	case map[string]interface{}:
		for k, vv := range t {
			if secretKeys[k] {
				if s, ok := vv.(string); ok && len(s) > 4 {
					t[k] = s[:4] + "…"
				}
				continue
			}
			redact(vv)
		}
	case []interface{}:
		for _, vv := range t {
			redact(vv)
		}
	}
}

// ---------------------------------------------------------------- clash api

func clashGet(path string, out interface{}) error {
	cl := http.Client{Timeout: 4 * time.Second}
	resp, err := cl.Get(clashBase + path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s: %s", resp.Status, b)
	}
	return json.Unmarshal(b, out)
}

func (r *Runner) clashInfo() {
	var ver map[string]interface{}
	if err := clashGet("/version", &ver); err != nil {
		r.log.Logf("[diag:clash] /version error: %v", err)
		return
	}
	r.log.Logf("[diag:clash] version=%v", ver)
	var px struct {
		Proxies map[string]struct {
			Type string   `json:"type"`
			Now  string   `json:"now"`
			All  []string `json:"all"`
		} `json:"proxies"`
	}
	if err := clashGet("/proxies", &px); err == nil {
		for name, p := range px.Proxies {
			if p.Now != "" || len(p.All) > 0 {
				r.log.Logf("[diag:clash] group %q type=%s now=%q all=%v", name, p.Type, p.Now, p.All)
			}
		}
	}
}

func (r *Runner) proxyDelay() {
	for _, u := range []string{"http://cp.cloudflare.com/generate_204", "https://discord.com/api/v9/gateway"} {
		var res struct {
			Delay   int    `json:"delay"`
			Message string `json:"message"`
		}
		err := clashGet("/proxies/proxy/delay?timeout=5000&url="+u, &res)
		r.log.Logf("[diag:delay] via proxy %s → %dms msg=%q err=%v", u, res.Delay, res.Message, err)
	}
}

type clashConn struct {
	ID       string `json:"id"`
	Metadata struct {
		Network     string `json:"network"`
		Type        string `json:"type"`
		SourceIP    string `json:"sourceIP"`
		SourcePort  string `json:"sourcePort"`
		DestIP      string `json:"destinationIP"`
		DestPort    string `json:"destinationPort"`
		Host        string `json:"host"`
		ProcessPath string `json:"processPath"`
		Process     string `json:"process"`
	} `json:"metadata"`
	Upload      int64    `json:"upload"`
	Download    int64    `json:"download"`
	Start       string   `json:"start"`
	Chains      []string `json:"chains"`
	Rule        string   `json:"rule"`
	RulePayload string   `json:"rulePayload"`
}

func (r *Runner) connections() {
	var res struct {
		DownloadTotal int64       `json:"downloadTotal"`
		UploadTotal   int64       `json:"uploadTotal"`
		Connections   []clashConn `json:"connections"`
		Memory        int64       `json:"memory"`
	}
	if err := clashGet("/connections", &res); err != nil {
		r.log.Logf("[diag:conns] error: %v", err)
		return
	}
	byProc := map[string]int{}
	udp := 0
	for _, c := range res.Connections {
		proc := c.Metadata.ProcessPath
		if proc == "" {
			proc = c.Metadata.Process
		}
		if i := strings.LastIndexAny(proc, `\/`); i >= 0 {
			proc = proc[i+1:]
		}
		byProc[proc+"/"+c.Metadata.Network]++
		if c.Metadata.Network == "udp" {
			udp++
		}
		// Full detail for UDP and anything Discord-related; TCP from other
		// apps is only counted, otherwise a browser floods the log.
		low := strings.ToLower(proc + c.Metadata.Host)
		if c.Metadata.Network == "udp" || strings.Contains(low, "discord") {
			r.log.Logf("[diag:conn] %s %s:%s → %s:%s host=%q proc=%q chains=%v rule=%q(%s) up=%d down=%d start=%s",
				c.Metadata.Network, c.Metadata.SourceIP, c.Metadata.SourcePort,
				c.Metadata.DestIP, c.Metadata.DestPort, c.Metadata.Host, proc,
				c.Chains, c.Rule, c.RulePayload, c.Upload, c.Download, c.Start)
		}
	}
	keys := make([]string, 0, len(byProc))
	for k := range byProc {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, byProc[k]))
	}
	r.log.Logf("[diag:conns] total=%d udp=%d up=%d down=%d mem=%d by_proc: %s",
		len(res.Connections), udp, res.UploadTotal, res.DownloadTotal, res.Memory, strings.Join(parts, " "))
}

// ---------------------------------------------------------------- stun

func (r *Runner) resolveStun() {
	var addrs []string
	for _, t := range stunTargets {
		host, port, _ := net.SplitHostPort(t)
		ips, err := net.LookupIP(host)
		if err != nil {
			r.log.Logf("[diag:stun] resolve %s: %v", host, err)
			continue
		}
		for _, ip := range ips {
			if ip.To4() != nil {
				addrs = append(addrs, net.JoinHostPort(ip.String(), port))
				r.log.Logf("[diag:stun] %s → %s", t, ip)
				break
			}
		}
	}
	r.mu.Lock()
	r.stunAddr = addrs
	r.mu.Unlock()
}

// stunList falls back to a literal Google STUN IP when Baseline has not
// resolved names yet (DNS inside the tunnel returns fake-ip).
func (r *Runner) stunList() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.stunAddr) == 0 {
		return []string{"74.125.250.129:19302"}
	}
	return r.stunAddr
}

func (r *Runner) stunRound(label string) {
	for _, a := range r.stunList() {
		r.log.Logf("[diag:stun/%s] %s %s", label, a, stunSeries(nil, a))
	}
}

func (r *Runner) stunBound(pi physIface) {
	for _, a := range r.stunList() {
		r.log.Logf("[diag:stun/bound %s %s] %s %s", pi.Name, pi.IP, a, stunSeries(pi.IP, a))
	}
}

// stunSeries sends 10 binding requests and reports RTT stats plus the
// mapped (public) address the server saw.
func stunSeries(local net.IP, addr string) string {
	raddr, err := net.ResolveUDPAddr("udp4", addr)
	if err != nil {
		return "resolve error: " + err.Error()
	}
	var laddr *net.UDPAddr
	if local != nil {
		laddr = &net.UDPAddr{IP: local}
	}
	conn, err := net.DialUDP("udp4", laddr, raddr)
	if err != nil {
		return "dial error: " + err.Error()
	}
	defer conn.Close()
	var rtts []float64
	lost := 0
	mapped := ""
	buf := make([]byte, 1500)
	for i := 0; i < 10; i++ {
		req := make([]byte, 20)
		binary.BigEndian.PutUint16(req[0:], 0x0001)
		binary.BigEndian.PutUint32(req[4:], 0x2112A442)
		_, _ = rand.Read(req[8:20])
		t := time.Now()
		if _, err := conn.Write(req); err != nil {
			return "write error: " + err.Error()
		}
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		got := false
		for {
			n, err := conn.Read(buf)
			if err != nil {
				break
			}
			if n >= 20 && string(buf[8:20]) == string(req[8:20]) {
				rtts = append(rtts, float64(time.Since(t).Microseconds())/1000)
				if m := parseMapped(buf[:n]); m != "" {
					mapped = m
				}
				got = true
				break
			}
		}
		if !got {
			lost++
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(rtts) == 0 {
		return fmt.Sprintf("ALL LOST (%d/10)", lost)
	}
	sort.Float64s(rtts)
	return fmt.Sprintf("ok=%d lost=%d min=%.0fms med=%.0fms max=%.0fms public=%s local=%s",
		len(rtts), lost, rtts[0], rtts[len(rtts)/2], rtts[len(rtts)-1], mapped, conn.LocalAddr())
}

// parseMapped extracts XOR-MAPPED-ADDRESS (0x0020) or MAPPED-ADDRESS (0x0001).
func parseMapped(b []byte) string {
	if len(b) < 20 {
		return ""
	}
	l := int(binary.BigEndian.Uint16(b[2:4]))
	attrs := b[20:]
	if len(attrs) > l {
		attrs = attrs[:l]
	}
	for len(attrs) >= 4 {
		typ := binary.BigEndian.Uint16(attrs[0:2])
		alen := int(binary.BigEndian.Uint16(attrs[2:4]))
		if 4+alen > len(attrs) {
			break
		}
		v := attrs[4 : 4+alen]
		if (typ == 0x0020 || typ == 0x0001) && alen >= 8 && v[1] == 0x01 {
			port := binary.BigEndian.Uint16(v[2:4])
			ip := net.IP(append([]byte(nil), v[4:8]...))
			if typ == 0x0020 {
				port ^= 0x2112
				for i := range ip {
					ip[i] ^= []byte{0x21, 0x12, 0xA4, 0x42}[i]
				}
			}
			return fmt.Sprintf("%s:%d", ip, port)
		}
		pad := (alen + 3) &^ 3
		if 4+pad > len(attrs) {
			break
		}
		attrs = attrs[4+pad:]
	}
	return ""
}

// ---------------------------------------------------------------- commands

type command struct {
	name string
	args []string
}

func runCommands(log Logger, stage string, cmds []command) {
	for _, c := range cmds {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, c.name, c.args...)
		hideWindow(cmd)
		out, err := cmd.CombinedOutput()
		cancel()
		label := c.args[len(c.args)-1]
		label = strings.TrimPrefix(label, "chcp 65001>nul & ")
		if i := strings.Index(label, "$ProgressPreference='SilentlyContinue'; "); i >= 0 {
			label = label[i+len("$ProgressPreference='SilentlyContinue'; "):]
		}
		if len(label) > 50 {
			label = label[:50] + "…"
		}
		tag := fmt.Sprintf("[diag:%s %s]", stage, label)
		if err != nil {
			log.Logf("%s error: %v", tag, err)
		}
		for _, ln := range strings.Split(decodeOutput(out), "\n") {
			ln = strings.TrimRight(ln, "\r ")
			if strings.TrimSpace(ln) == "" {
				continue
			}
			log.Logf("%s %s", tag, ln)
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
