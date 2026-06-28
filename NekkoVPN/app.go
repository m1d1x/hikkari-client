package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// App is the Wails backend. It manages the sing-box core process and config.json.
type App struct {
	ctx     context.Context
	mu      sync.Mutex
	cmd     *exec.Cmd
	baseDir string

	logFile  *os.File
	logger   *log.Logger
	logMu    sync.Mutex
	logBytes int64

	healthMu    sync.Mutex
	directIP    string // last-known IP when VPN was provably OFF (= ISP IP)
	lastIP      string
	lastStatus  string // "off" | "connecting" | "ok" | "broken"
	lastChecked time.Time

	upMu       sync.RWMutex
	uptimeHist map[string][]PingSample
}

func NewApp() *App {
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.baseDir = a.findBaseDir()
	a.initLogger()
	a.loadDirectIP()
	a.ensureProfiles()
	a.loadUptime()
	go a.pingLoop()
	a.log("=== NekkoVPN startup, baseDir=%s ===", a.baseDir)
}

// shutdown is wired to Wails OnShutdown — ensures sing-box dies with the GUI.
func (a *App) shutdown(ctx context.Context) {
	a.log("=== NekkoVPN shutdown: stopping core ===")
	_ = a.StopVPN()
}

// findBaseDir locates the folder that holds sing-box.exe + config.json.
func (a *App) findBaseDir() string {
	var candidates []string
	if exe, err := os.Executable(); err == nil {
		d := filepath.Dir(exe)
		candidates = append(candidates, d, filepath.Dir(d), filepath.Dir(filepath.Dir(d)))
	}
	candidates = append(candidates, `C:\Users\CRYBVBY\PROJECTS\NEKOBOX`)
	for _, c := range candidates {
		if fileExists(filepath.Join(c, "sing-box.exe")) && fileExists(filepath.Join(c, "config.json")) {
			return c
		}
	}
	return `C:\Users\CRYBVBY\PROJECTS\NEKOBOX`
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func (a *App) singboxPath() string  { return filepath.Join(a.baseDir, "sing-box.exe") }
func (a *App) configPath() string   { return filepath.Join(a.baseDir, "config.json") }
func (a *App) profilesPath() string { return filepath.Join(a.baseDir, "profiles.json") }
func (a *App) logPath() string      { return filepath.Join(a.baseDir, "nekko-vpn.log") }
func (a *App) directIPPath() string { return filepath.Join(a.baseDir, "direct-ip.txt") }

// ============================================================
//  Logging — append to nekko-vpn.log, rotate once past ~512 KB
// ============================================================

const maxLogBytes = 512 * 1024

func (a *App) initLogger() {
	p := a.logPath()
	if fi, err := os.Stat(p); err == nil && fi.Size() > maxLogBytes {
		_ = os.Rename(p, p+".old")
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		// fall back to stderr — never block startup over a log file
		a.logger = log.New(os.Stderr, "", log.LstdFlags|log.Lmicroseconds)
		return
	}
	a.logFile = f
	if fi, err := f.Stat(); err == nil {
		a.logBytes = fi.Size()
	}
	a.logger = log.New(f, "", log.LstdFlags|log.Lmicroseconds)
}

func (a *App) log(format string, args ...interface{}) {
	a.logMu.Lock()
	defer a.logMu.Unlock()
	if a.logger == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	a.logger.Println(msg)
	a.logBytes += int64(len(msg)) + 32
	if a.logFile != nil && a.logBytes > maxLogBytes {
		_ = a.logFile.Close()
		_ = os.Rename(a.logPath(), a.logPath()+".old")
		f, err := os.OpenFile(a.logPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err == nil {
			a.logFile = f
			a.logger = log.New(f, "", log.LstdFlags|log.Lmicroseconds)
			a.logBytes = 0
		}
	}
}

// GetLogTail returns the last ~maxBytes of the log file, for in-app debugging.
func (a *App) GetLogTail() string {
	data, err := os.ReadFile(a.logPath())
	if err != nil {
		return ""
	}
	const tail = 16 * 1024
	if len(data) > tail {
		data = data[len(data)-tail:]
	}
	return string(data)
}

// ============================================================
//  VPN control
// ============================================================

const createNoWindow = 0x08000000

// StartVPN launches the sing-box core (hidden). Requires admin (TUN).
func (a *App) StartVPN() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.isAlive() {
		a.log("StartVPN: core already alive (pid=%d), no-op", a.cmd.Process.Pid)
		return nil
	}
	cmd := exec.Command(a.singboxPath(), "run", "-c", a.configPath())
	cmd.Dir = a.baseDir
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}

	// Pipe sing-box stdout/stderr into our log so we can see why the tunnel died.
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		a.log("StartVPN: failed to start sing-box: %v", err)
		return fmt.Errorf("не удалось запустить sing-box: %w", err)
	}
	a.cmd = cmd
	a.log("StartVPN: sing-box started, pid=%d", cmd.Process.Pid)

	if stdout != nil {
		go a.pipeLog("singbox.out", stdout)
	}
	if stderr != nil {
		go a.pipeLog("singbox.err", stderr)
	}

	go func(c *exec.Cmd) {
		err := c.Wait()
		a.log("sing-box exited: err=%v, state=%v", err, c.ProcessState)
	}(cmd)
	return nil
}

func (a *App) pipeLog(tag string, r io.ReadCloser) {
	defer r.Close()
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			for _, ln := range strings.Split(strings.TrimRight(string(buf[:n]), "\n"), "\n") {
				if ln = strings.TrimSpace(ln); ln != "" {
					a.log("[%s] %s", tag, ln)
				}
			}
		}
		if err != nil {
			return
		}
	}
}

// StopVPN stops the core. Kills our child and any stray sing-box.
func (a *App) StopVPN() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cmd != nil && a.cmd.Process != nil {
		a.log("StopVPN: killing sing-box pid=%d", a.cmd.Process.Pid)
		_ = a.cmd.Process.Kill()
		a.cmd = nil
	}
	// belt-and-suspenders: also kill any sing-box started elsewhere (old tray)
	kill := exec.Command("taskkill", "/F", "/IM", "sing-box.exe")
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	_ = kill.Run()
	a.log("StopVPN: done")
	return nil
}

// RestartVPN applies config changes by cycling the core.
func (a *App) RestartVPN() error {
	a.log("RestartVPN: begin")
	if err := a.StopVPN(); err != nil {
		a.log("RestartVPN: stop error: %v", err)
		return err
	}
	time.Sleep(1500 * time.Millisecond)
	err := a.StartVPN()
	a.log("RestartVPN: end, err=%v", err)
	return err
}

func (a *App) isAlive() bool {
	return a.cmd != nil && a.cmd.Process != nil && a.cmd.ProcessState == nil
}

// IsRunning is the source of truth for the UI: Clash API answers only if core is up.
// NOTE: this only tells you the *process* is alive — it does NOT tell you the tunnel
// is actually carrying traffic. Use Health() for that.
func (a *App) IsRunning() bool {
	client := http.Client{Timeout: 600 * time.Millisecond}
	resp, err := client.Get("http://127.0.0.1:9090/version")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}

// ExternalIP returns the current egress IP (shows whether you're on VPN).
func (a *App) ExternalIP() string {
	ip, _ := a.fetchExternalIP()
	if ip == "" {
		return "—"
	}
	return ip
}

// freshHTTPClient returns a client whose every request opens a brand-new TCP
// connection. Critical for the egress-IP probe: if we reused a connection that
// was established before sing-box took over routing, ipify would keep replying
// with the ISP IP forever even though the tunnel is now up.
func freshHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DisableKeepAlives: true,
			Proxy:             nil,
		},
	}
}

func (a *App) fetchExternalIP() (string, error) {
	resp, err := freshHTTPClient(4 * time.Second).Get("https://api.ipify.org")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	ip := strings.TrimSpace(string(b))
	if net.ParseIP(ip) == nil {
		return "", fmt.Errorf("bad ip response: %q", ip)
	}
	return ip, nil
}

// ============================================================
//  Health — real tunnel health, not just "is the process alive"
// ============================================================

// HealthInfo is what the UI consumes. Status is the single source of truth.
type HealthInfo struct {
	Running  bool   `json:"running"`  // sing-box process answers Clash API
	TunnelOK bool   `json:"tunnelOK"` // egress IP differs from the cached ISP IP
	IP       string `json:"ip"`       // current egress IP, or "" on failure
	Status   string `json:"status"`   // "off" | "connecting" | "ok" | "broken"
	Message  string `json:"message"`  // human-readable reason, esp. for "broken"
}

// loadDirectIP reads the cached ISP IP from disk (captured when VPN was OFF).
func (a *App) loadDirectIP() {
	b, err := os.ReadFile(a.directIPPath())
	if err != nil {
		return
	}
	ip := strings.TrimSpace(string(b))
	if net.ParseIP(ip) != nil {
		a.healthMu.Lock()
		a.directIP = ip
		a.healthMu.Unlock()
		a.log("loadDirectIP: cached ISP IP = %s", ip)
	}
}

func (a *App) saveDirectIP(ip string) {
	_ = os.WriteFile(a.directIPPath(), []byte(ip), 0644)
}

// isOurVPNServer returns true if ip is the peer address of any profile —
// i.e. an IP we'd see as our own egress when the tunnel works. Such an IP
// must never be cached as the "ISP baseline".
func (a *App) isOurVPNServer(ip string) bool {
	pf, err := a.readProfiles()
	if err != nil {
		return false
	}
	for _, p := range pf.Profiles {
		if host := peerHost(p.Endpoint); host == ip {
			return true
		}
	}
	return false
}

// Health performs a probe and returns the canonical state of the connection.
//   - core down              → "off"
//   - core up, no egress yet → "connecting"
//   - core up, egress == ISP → "broken" (tunnel leaked / dead)
//   - core up, egress != ISP → "ok"
func (a *App) Health() HealthInfo {
	hi := HealthInfo{}
	hi.Running = a.IsRunning()

	if !hi.Running {
		// VPN is off — this is the moment to refresh our baseline ISP IP.
		hi.Status = "off"
		hi.Message = "ядро не запущено"
		if ip, err := a.fetchExternalIP(); err == nil {
			hi.IP = ip
			// Guard: never let a VPN-server IP become the "ISP baseline".
			// This can happen during shutdown windows where TUN still routes
			// even though Clash API briefly stops answering — without the
			// guard, the next Health() would see egress == baseline and
			// flip the UI to "broken" forever.
			if a.isOurVPNServer(ip) {
				a.log("Health: refusing to set baseline ISP IP to known VPN server %q", ip)
			} else {
				a.healthMu.Lock()
				if a.directIP != ip {
					a.log("Health: baseline ISP IP updated %q -> %q", a.directIP, ip)
					a.directIP = ip
					a.saveDirectIP(ip)
				}
				a.healthMu.Unlock()
			}
		}
		a.transition("off", hi.IP, hi.Message)
		return hi
	}

	ip, err := a.fetchExternalIP()
	if err != nil || ip == "" {
		hi.Status = "connecting"
		hi.Message = "проверяю выход в сеть"
		if err != nil {
			hi.Message = fmt.Sprintf("нет ответа от api.ipify.org: %v", err)
		}
		a.transition("connecting", "", hi.Message)
		return hi
	}
	hi.IP = ip

	a.healthMu.Lock()
	baseline := a.directIP
	a.healthMu.Unlock()

	if baseline != "" && ip == baseline {
		hi.Status = "broken"
		hi.Message = "трафик уходит мимо туннеля (IP = ISP)"
		a.transition("broken", ip, hi.Message)
		return hi
	}
	hi.TunnelOK = true
	hi.Status = "ok"
	a.transition("ok", ip, "")
	return hi
}

func (a *App) transition(status, ip, msg string) {
	a.healthMu.Lock()
	defer a.healthMu.Unlock()
	a.lastChecked = time.Now()
	if status != a.lastStatus || ip != a.lastIP {
		a.log("health: %s -> %s (ip=%q, %s)", a.lastStatus, status, ip, msg)
		a.lastStatus = status
		a.lastIP = ip
	}
}

// ============================================================
//  Whitelist (config.json route rules) — three categories
// ============================================================

// Whitelist holds the three "direct" (bypass-VPN) lists shown in the UI.
type Whitelist struct {
	Domains   []string `json:"domains"`
	IPs       []string `json:"ips"`
	Processes []string `json:"processes"`
}

// category -> the JSON rule key that holds it
var ruleKeyByCategory = map[string]string{
	"domains":   "domain_suffix",
	"ips":       "ip_cidr",
	"processes": "process_name",
}

// GetWhitelist reads the three direct lists out of config.json.
func (a *App) GetWhitelist() (Whitelist, error) {
	cfg, err := a.readConfig()
	if err != nil {
		return Whitelist{}, err
	}
	wl := Whitelist{Domains: []string{}, IPs: []string{}, Processes: []string{}}
	for _, rule := range a.directRules(cfg) {
		wl.Domains = appendStrings(wl.Domains, rule["domain_suffix"])
		wl.IPs = appendStrings(wl.IPs, rule["ip_cidr"])
		wl.Processes = appendStrings(wl.Processes, rule["process_name"])
	}
	return wl, nil
}

// AddRule adds a value to a whitelist category (domains|ips|processes) and saves.
func (a *App) AddRule(category, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("пустое значение")
	}
	key, ok := ruleKeyByCategory[category]
	if !ok {
		return fmt.Errorf("неизвестная категория: %s", category)
	}
	cfg, err := a.readConfig()
	if err != nil {
		return err
	}
	rule := a.findOrCreateDirectRule(cfg, key)
	list := toStringSlice(rule[key])
	for _, v := range list {
		if strings.EqualFold(v, value) {
			return nil // already present
		}
	}
	rule[key] = append(list, value)
	return a.writeConfig(cfg)
}

// RemoveRule removes a value from a whitelist category and saves.
func (a *App) RemoveRule(category, value string) error {
	key, ok := ruleKeyByCategory[category]
	if !ok {
		return fmt.Errorf("неизвестная категория: %s", category)
	}
	cfg, err := a.readConfig()
	if err != nil {
		return err
	}
	for _, rule := range a.directRules(cfg) {
		if _, has := rule[key]; !has {
			continue
		}
		list := toStringSlice(rule[key])
		out := list[:0]
		for _, v := range list {
			if !strings.EqualFold(v, value) {
				out = append(out, v)
			}
		}
		rule[key] = out
	}
	return a.writeConfig(cfg)
}

// ============================================================
//  Profiles — switch the SERVER only, config stays shared
// ============================================================
//
// A profile is just one AWG endpoint block (keys + endpoint + obfuscation).
// Switching swaps cfg["endpoints"] and nothing else: route / dns / split
// rules (the "whitelist") are shared across all servers. The endpoint tag is
// forced to "awg-ep" so route.final and dns.detour keep matching.

// Profile is one server entry stored in profiles.json.
type Profile struct {
	Name     string                 `json:"name"`
	Label    string                 `json:"label"`
	Endpoint map[string]interface{} `json:"endpoint"`
}

type profilesFile struct {
	Active   string    `json:"active"`
	Profiles []Profile `json:"profiles"`
}

// ProfileInfo is the lightweight shape the UI renders.
type ProfileInfo struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Active   bool   `json:"active"`
	Endpoint string `json:"endpoint"` // "host:port" for UI; empty if missing
	Protocol string `json:"protocol"` // pretty protocol name ("AmneziaWG", "WireGuard", ...)
}

const sharedEndpointTag = "awg-ep"

func (a *App) readProfiles() (*profilesFile, error) {
	raw, err := os.ReadFile(a.profilesPath())
	if err != nil {
		return nil, err
	}
	var pf profilesFile
	if err := json.Unmarshal(raw, &pf); err != nil {
		return nil, fmt.Errorf("profiles.json parse: %w", err)
	}
	return &pf, nil
}

func (a *App) writeProfiles(pf *profilesFile) error {
	out, err := json.MarshalIndent(pf, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(a.profilesPath(), out, 0644)
}

// ensureProfiles seeds profiles.json from the current config.json (as "main")
// if the file is missing, so the UI always has at least one profile to show.
func (a *App) ensureProfiles() {
	if fileExists(a.profilesPath()) {
		return
	}
	cfg, err := a.readConfig()
	if err != nil {
		return
	}
	eps, _ := cfg["endpoints"].([]interface{})
	if len(eps) == 0 {
		return
	}
	ep, ok := eps[0].(map[string]interface{})
	if !ok {
		return
	}
	_ = a.writeProfiles(&profilesFile{
		Active:   "main",
		Profiles: []Profile{{Name: "main", Label: "Основной", Endpoint: ep}},
	})
}

// GetProfiles returns all profiles with the active one flagged.
func (a *App) GetProfiles() ([]ProfileInfo, error) {
	pf, err := a.readProfiles()
	if err != nil {
		return nil, err
	}
	out := make([]ProfileInfo, 0, len(pf.Profiles))
	for _, p := range pf.Profiles {
		out = append(out, ProfileInfo{
			Name:     p.Name,
			Label:    p.Label,
			Active:   p.Name == pf.Active,
			Endpoint: peerEndpoint(p.Endpoint),
			Protocol: prettyProtocol(p.Endpoint),
		})
	}
	return out, nil
}

func prettyProtocol(ep map[string]interface{}) string {
	t, _ := ep["type"].(string)
	switch strings.ToLower(t) {
	case "awg":
		return "AmneziaWG"
	case "wireguard":
		return "WireGuard"
	case "":
		return "—"
	default:
		return strings.ToUpper(t)
	}
}

// RenameProfile sets a new human label on an existing profile.
func (a *App) RenameProfile(name, newLabel string) error {
	newLabel = strings.TrimSpace(newLabel)
	if newLabel == "" {
		return fmt.Errorf("название не может быть пустым")
	}
	pf, err := a.readProfiles()
	if err != nil {
		return err
	}
	for i := range pf.Profiles {
		if pf.Profiles[i].Name == name {
			pf.Profiles[i].Label = newLabel
			return a.writeProfiles(pf)
		}
	}
	return fmt.Errorf("профиль %q не найден", name)
}

// DeleteProfile removes a profile by name. Refuses to delete the last one
// or the currently active one (caller must switch first).
func (a *App) DeleteProfile(name string) error {
	pf, err := a.readProfiles()
	if err != nil {
		return err
	}
	if len(pf.Profiles) <= 1 {
		return fmt.Errorf("нельзя удалить единственный профиль")
	}
	if pf.Active == name {
		return fmt.Errorf("сначала переключитесь на другой профиль")
	}
	out := pf.Profiles[:0]
	found := false
	for _, p := range pf.Profiles {
		if p.Name == name {
			found = true
			continue
		}
		out = append(out, p)
	}
	if !found {
		return fmt.Errorf("профиль %q не найден", name)
	}
	pf.Profiles = out
	// also drop its uptime history so the file stays tidy
	a.upMu.Lock()
	delete(a.uptimeHist, name)
	a.upMu.Unlock()
	a.saveUptime()
	return a.writeProfiles(pf)
}

// peerEndpoint formats "host:port" out of an AWG endpoint map.
func peerEndpoint(ep map[string]interface{}) string {
	peers, ok := ep["peers"].([]interface{})
	if !ok || len(peers) == 0 {
		return ""
	}
	p0, ok := peers[0].(map[string]interface{})
	if !ok {
		return ""
	}
	host, _ := p0["address"].(string)
	if host == "" {
		return ""
	}
	switch v := p0["port"].(type) {
	case float64:
		return fmt.Sprintf("%s:%d", host, int(v))
	case int:
		return fmt.Sprintf("%s:%d", host, v)
	default:
		return host
	}
}

// ActiveProfile returns the name of the currently selected profile.
func (a *App) ActiveProfile() (string, error) {
	pf, err := a.readProfiles()
	if err != nil {
		return "", err
	}
	return pf.Active, nil
}

// SwitchProfile swaps ONLY the endpoint block in config.json to the chosen
// server, marks it active, and (if the core is up) restarts to apply.
func (a *App) SwitchProfile(name string) error {
	pf, err := a.readProfiles()
	if err != nil {
		return err
	}
	var target *Profile
	for i := range pf.Profiles {
		if pf.Profiles[i].Name == name {
			target = &pf.Profiles[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("профиль не найден: %s", name)
	}
	if target.Endpoint == nil {
		return fmt.Errorf("у профиля %q нет данных сервера", name)
	}

	cfg, err := a.readConfig()
	if err != nil {
		return err
	}
	// keep the shared tag so route.final / dns.detour stay valid
	ep := target.Endpoint
	ep["tag"] = sharedEndpointTag
	cfg["endpoints"] = []interface{}{ep}
	if err := a.writeConfig(cfg); err != nil {
		return err
	}

	pf.Active = name
	if err := a.writeProfiles(pf); err != nil {
		return err
	}

	if a.IsRunning() {
		return a.RestartVPN()
	}
	return nil
}

// ============================================================
//  config.json helpers (tolerant of // comments)
// ============================================================

func (a *App) readConfig() (map[string]interface{}, error) {
	raw, err := os.ReadFile(a.configPath())
	if err != nil {
		return nil, err
	}
	clean := stripLineComments(raw)
	var cfg map[string]interface{}
	if err := json.Unmarshal(clean, &cfg); err != nil {
		return nil, fmt.Errorf("config.json parse: %w", err)
	}
	return cfg, nil
}

func (a *App) writeConfig(cfg map[string]interface{}) error {
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(a.configPath(), out, 0644)
}

// directRules returns route.rules entries that route to outbound "direct".
func (a *App) directRules(cfg map[string]interface{}) []map[string]interface{} {
	var out []map[string]interface{}
	route, _ := cfg["route"].(map[string]interface{})
	if route == nil {
		return out
	}
	rules, _ := route["rules"].([]interface{})
	for _, r := range rules {
		rule, _ := r.(map[string]interface{})
		if rule == nil {
			continue
		}
		if ob, _ := rule["outbound"].(string); ob == "direct" {
			out = append(out, rule)
		}
	}
	return out
}

// findOrCreateDirectRule returns the direct rule holding `key`, creating one if needed.
func (a *App) findOrCreateDirectRule(cfg map[string]interface{}, key string) map[string]interface{} {
	for _, rule := range a.directRules(cfg) {
		if _, has := rule[key]; has {
			return rule
		}
	}
	// none yet — create a fresh direct rule with this key
	route, _ := cfg["route"].(map[string]interface{})
	if route == nil {
		route = map[string]interface{}{}
		cfg["route"] = route
	}
	rules, _ := route["rules"].([]interface{})
	newRule := map[string]interface{}{key: []interface{}{}, "outbound": "direct"}
	route["rules"] = append(rules, newRule)
	return newRule
}

// ============================================================
//  small utils
// ============================================================

// stripLineComments removes whole-line // comments (our config style), so
// encoding/json can parse the JSONC config. Values never contain "//".
func stripLineComments(raw []byte) []byte {
	lines := strings.Split(string(raw), "\n")
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), "//") {
			continue
		}
		out = append(out, ln)
	}
	return []byte(strings.Join(out, "\n"))
}

func toStringSlice(v interface{}) []string {
	arr, _ := v.([]interface{})
	out := make([]string, 0, len(arr))
	for _, it := range arr {
		if s, ok := it.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func appendStrings(dst []string, v interface{}) []string {
	return append(dst, toStringSlice(v)...)
}
