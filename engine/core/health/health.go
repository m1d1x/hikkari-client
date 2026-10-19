// Package health classifies the tunnel state into one of four canonical
// statuses the UI consumes: "off", "connecting", "ok", "broken". It also
// maintains a baseline ISP IP captured while VPN was provably OFF — that
// baseline is how we know later that the tunnel "leaked" (egress == ISP).
package health

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"NekkoVPN/core/vpn"
)

// RefreshInterval is how often the background loop reprobes. Health() —
// the public read — is now instant; the actual I/O happens here. Each pass
// only asks the local Clash API whether the core is alive; the egress IP is
// fetched far less often (see EgressInterval).
const RefreshInterval = 2 * time.Second

// EgressInterval is how often the egress IP is re-fetched while nothing
// changes. Every fetch goes through the tunnel, and at the old 2s cadence
// this probe alone was ~30 tunnel sessions a minute competing with user
// traffic (Discord voice) for the same transport. It is also fetched at
// once whenever the core comes up or goes down, or the last probe failed.
const EgressInterval = 60 * time.Second

// Consecutive-failure thresholds. A single failed probe means nothing: the
// Clash API can miss its 2s budget while the core is busy, and one HTTPS
// request through a loaded tunnel times out routinely. Reacting to either
// immediately is what made the UI flap between ok/connecting/off (and, far
// worse, let a momentary "off" poison the ISP baseline). We only believe a
// bad reading after it repeats.
const (
	downStreakThreshold   = 3 // ~6s of an unanswering core before "off"
	egressStreakThreshold = 3 // ~6s of failed egress probes before "connecting"
)

// refusedBaselineInterval is how often a still-standing baseline refusal
// repeats itself in the log. Long enough that a stuck core cannot spam the
// file (the probe loop would otherwise write 30 lines a minute), short enough
// that any usefully-sized tail of the log contains one.
const refusedBaselineInterval = 5 * time.Minute

// Info is the canonical state the UI consumes.
type Info struct {
	Running  bool   `json:"running"`  // sing-box process answers Clash API
	TunnelOK bool   `json:"tunnelOK"` // egress IP differs from cached ISP IP
	IP       string `json:"ip"`       // current egress IP, or "" on failure
	Status   string `json:"status"`   // "off" | "connecting" | "ok" | "broken"
	Message  string `json:"message"`  // human-readable reason, esp. for "broken"
}

// Logger is the minimal logging surface health needs.
type Logger interface {
	Logf(format string, args ...interface{})
}

// VPNHostProvider yields the current set of known VPN server hosts. We use
// it to guard against ever caching one of them as the "ISP baseline" — that
// would permanently flip the UI to "broken".
type VPNHostProvider func() []string

// Checker is the stateful health probe. The actual probing happens in a
// background loop started by [Run]; [Probe] returns the most recent cached
// Info instantly. This is critical for FFI consumers (Flutter) — synchronous
// FFI calls block the caller's UI thread, so anything that does I/O on the
// call path will jank the app.
type Checker struct {
	directIPPath string
	log          Logger
	vpnHosts     VPNHostProvider

	mu          sync.Mutex
	directIP    string // last-known IP captured while VPN was provably OFF
	lastIP      string
	lastStatus  string
	lastChecked time.Time

	downStreak   int    // consecutive "Clash API silent" readings
	egressStreak int    // consecutive failed egress probes
	tunnelIP     string // last egress IP seen while the tunnel was up

	// lastRefusedBaseline is the IP the last "refusing to baseline" line was
	// about, and lastRefusedBaselineAt is when that line went out. The probe
	// loop runs every 2s, and a core stuck down while the tunnel's address is
	// still what the network answers with hits that refusal on every single
	// pass — a few minutes of it buried the actual failure under two hundred
	// identical lines. So the line is rate-limited rather than deduplicated
	// outright: a reader who only has the tail of a rotated log still sees
	// that the guard is active, and how long it has been.
	lastRefusedBaseline   string
	lastRefusedBaselineAt time.Time
	refusedBaselineRuns   int // suppressed repeats since the last line

	// resolvedHosts caches VPN hostname → IPs so isOurVPNServer works for
	// profiles configured with a domain instead of a literal address.
	resolvedHosts   map[string]bool
	resolvedHostsAt time.Time

	cached Info // last full Probe result; served by [Probe]

	lastEgressAt time.Time // when the egress IP was last fetched
	lastRunning  bool      // core liveness seen by the previous pass
}

func New(directIPPath string, log Logger, vpnHosts VPNHostProvider) *Checker {
	return &Checker{
		directIPPath: directIPPath,
		log:          log,
		vpnHosts:     vpnHosts,
	}
}

// LoadDirectIP reads the cached ISP IP from disk so the baseline persists
// across launches.
func (c *Checker) LoadDirectIP() {
	b, err := os.ReadFile(c.directIPPath)
	if err != nil {
		return
	}
	ip := strings.TrimSpace(string(b))
	if net.ParseIP(ip) != nil {
		c.mu.Lock()
		c.directIP = ip
		c.mu.Unlock()
		c.log.Logf("health.LoadDirectIP: cached ISP IP = %s", ip)
	}
}

func (c *Checker) saveDirectIP(ip string) {
	_ = os.WriteFile(c.directIPPath, []byte(ip), 0644)
}

// ExternalIP returns the cached egress IP from the last background probe,
// or "—" if we don't have one yet. Lock-free fast path — no I/O.
func (c *Checker) ExternalIP() string {
	c.mu.Lock()
	ip := c.cached.IP
	c.mu.Unlock()
	if ip == "" {
		return "—"
	}
	return ip
}

// Probe returns the most recent cached health snapshot. Lock-free fast
// path — safe to call at arbitrary frequency from the FFI boundary. The
// actual work is done in [Run] on a background goroutine.
func (c *Checker) Probe() Info {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cached
}

// Run is the background refresh loop. Cancel ctx to stop. Call this from
// Core.Init in its own goroutine — it blocks until ctx is done.
func (c *Checker) Run(ctx context.Context) {
	// Prime the cache with a synchronous first probe so the very first
	// Probe() call after Init returns real data instead of an empty zero.
	c.refresh()
	ticker := time.NewTicker(RefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.refresh()
		}
	}
}

func (c *Checker) refresh() {
	hi := c.probeOnce()
	c.mu.Lock()
	c.cached = hi
	c.mu.Unlock()
}

// probeOnce does the actual probing — was the public Probe before we
// added caching. Returns the canonical Info:
//
//	core down              → "off"
//	core up, no egress yet → "connecting"
//	core up, egress == ISP → "broken"
//	core up, egress != ISP → "ok"
func (c *Checker) probeOnce() Info {
	hi := Info{}
	hi.Running = vpn.ClashAPIRunning()

	c.mu.Lock()
	if hi.Running {
		c.downStreak = 0
	} else {
		c.downStreak++
	}
	downStreak := c.downStreak
	cached := c.cached
	c.mu.Unlock()

	// Core silent, but not for long enough to be believed: hold the last
	// known state instead of announcing "off" (and, crucially, instead of
	// re-baselining the ISP IP off a tunnel that is still up).
	if !hi.Running && downStreak < downStreakThreshold && cached.Status != "" {
		return cached
	}

	// Core started or stopped since the last pass: connections kept alive
	// from the other side of that change would report the wrong path (an
	// ISP-routed connection reused after the tunnel came up keeps answering
	// with the ISP IP), so drop them and re-fetch right away.
	c.mu.Lock()
	flipped := hi.Running != c.lastRunning
	c.lastRunning = hi.Running
	due := flipped || time.Since(c.lastEgressAt) >= EgressInterval
	c.mu.Unlock()
	if flipped {
		egressTransport.CloseIdleConnections()
	}
	if !due && cached.Status != "" && cached.Status != "connecting" && cached.Running == hi.Running {
		return cached
	}
	c.mu.Lock()
	c.lastEgressAt = time.Now()
	c.mu.Unlock()

	if !hi.Running {
		hi.Status = "off"
		hi.Message = "ядро не запущено"
		if ip, err := fetchExternalIP(); err == nil {
			hi.IP = ip
			c.maybeBaseline(ip)
		}
		c.transition("off", hi.IP, hi.Message)
		return hi
	}

	ip, err := fetchExternalIP()
	if err != nil || ip == "" {
		c.mu.Lock()
		// Retry on the next pass instead of in EgressInterval: the streak
		// threshold below counts consecutive failures, not minutes.
		c.lastEgressAt = time.Time{}
		c.egressStreak++
		streak := c.egressStreak
		c.mu.Unlock()
		// Same reasoning as above: one timed-out request through a busy
		// tunnel is not a state change.
		if streak < egressStreakThreshold && cached.Status == "ok" {
			return cached
		}
		hi.Status = "connecting"
		hi.Message = "проверяю выход в сеть"
		if err != nil {
			hi.Message = fmt.Sprintf("нет ответа от сервисов проверки ip: %v", err)
		}
		c.transition("connecting", "", hi.Message)
		return hi
	}
	c.mu.Lock()
	c.egressStreak = 0
	c.mu.Unlock()
	hi.IP = ip

	c.mu.Lock()
	baseline := c.directIP
	c.mu.Unlock()

	if baseline != "" && ip == baseline {
		hi.Status = "broken"
		hi.Message = "трафик уходит мимо туннеля (IP = ISP)"
		c.transition("broken", ip, hi.Message)
		return hi
	}
	hi.TunnelOK = true
	hi.Status = "ok"
	c.mu.Lock()
	c.tunnelIP = ip
	c.mu.Unlock()
	c.transition("ok", ip, "")
	return hi
}

// maybeBaseline records ip as the ISP baseline — the IP we compare against
// later to detect a leaking tunnel. Two guards, because a wrong baseline is
// unrecoverable without deleting state: the UI would read every subsequent
// (correct) egress IP as a leak and sit on "broken" forever.
//
//   - never adopt a known VPN server address;
//   - never adopt an IP we have seen as a tunnel egress in this session.
func (c *Checker) maybeBaseline(ip string) {
	if c.isOurVPNServer(ip) {
		c.logRefusal(ip, "known VPN server")
		return
	}
	c.mu.Lock()
	if ip == c.tunnelIP {
		c.mu.Unlock()
		c.logRefusal(ip, "last tunnel egress")
		return
	}
	c.lastRefusedBaseline = ""
	c.lastRefusedBaselineAt = time.Time{}
	c.refusedBaselineRuns = 0
	if c.directIP != ip {
		c.log.Logf("health: baseline ISP IP updated %q -> %q", c.directIP, ip)
		c.directIP = ip
		c.saveDirectIP(ip)
	}
	c.mu.Unlock()
}

// logRefusal reports a rejected baseline candidate: immediately when the
// address is new, then at most once per refusedBaselineInterval for as long as
// the same address keeps being refused. The periodic line carries how many
// probes were suppressed, so a log that lost its earlier lines to rotation
// still says the guard is holding and roughly since when.
func (c *Checker) logRefusal(ip, why string) {
	now := time.Now()

	c.mu.Lock()
	sameIP := c.lastRefusedBaseline == ip
	suppressed := c.refusedBaselineRuns
	quiet := now.Sub(c.lastRefusedBaselineAt)
	if sameIP && quiet < refusedBaselineInterval {
		c.refusedBaselineRuns++
		c.mu.Unlock()
		return
	}
	c.lastRefusedBaseline = ip
	c.lastRefusedBaselineAt = now
	c.refusedBaselineRuns = 0
	c.mu.Unlock()

	if !sameIP {
		c.log.Logf("health: refusing to set baseline ISP IP to %s %q", why, ip)
		return
	}
	c.log.Logf("health: still refusing to set baseline ISP IP to %s %q (%d probes suppressed in the last %s)",
		why, ip, suppressed, quiet.Round(time.Second))
}

// isOurVPNServer reports whether ip belongs to one of the configured VPN
// servers. Profiles may carry a hostname rather than an address (a vless
// link pointing at a domain, say), so plain string equality is not enough —
// those get resolved, and the answer is cached for a minute so the guard
// costs nothing on the 2s probe loop.
func (c *Checker) isOurVPNServer(ip string) bool {
	if c.vpnHosts == nil {
		return false
	}
	hosts := c.vpnHosts()
	var toResolve []string
	for _, h := range hosts {
		if h == ip {
			return true
		}
		if net.ParseIP(h) == nil {
			toResolve = append(toResolve, h)
		}
	}
	if len(toResolve) == 0 {
		return false
	}

	c.mu.Lock()
	fresh := time.Since(c.resolvedHostsAt) < time.Minute && c.resolvedHosts != nil
	known := c.resolvedHosts[ip]
	c.mu.Unlock()
	if fresh {
		return known
	}

	resolved := map[string]bool{}
	for _, h := range toResolve {
		addrs, err := net.LookupHost(h)
		if err != nil {
			continue
		}
		for _, a := range addrs {
			resolved[a] = true
		}
	}
	c.mu.Lock()
	c.resolvedHosts = resolved
	c.resolvedHostsAt = time.Now()
	c.mu.Unlock()
	return resolved[ip]
}

func (c *Checker) transition(status, ip, msg string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastChecked = time.Now()
	if status != c.lastStatus || ip != c.lastIP {
		c.log.Logf("health: %s -> %s (ip=%q, %s)", c.lastStatus, status, ip, msg)
		c.lastStatus = status
		c.lastIP = ip
	}
}

// egressTransport keeps the egress probe's connections alive between
// fetches, so a steady tunnel costs no new session per probe. A connection
// opened before sing-box took over routing would keep reporting the ISP IP,
// so probeOnce drops idle connections whenever the core starts or stops.
var egressTransport = &http.Transport{
	Proxy:           nil,
	IdleConnTimeout: 3 * EgressInterval,
}

func egressHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: egressTransport}
}

// echoServices are the plain-text "what is my IP" endpoints the egress probe
// uses, in order. Several, because a single one is not a reliable signal:
// through some exit servers api.ipify.org times out routinely while the
// tunnel is perfectly fine, and the probe reading that as a failure is what
// made the UI flap ok -> connecting -> ok every half-minute. They must all
// answer with a bare address and nothing else, so the parsing below stays
// shared.
var echoServices = []string{
	"https://api.ipify.org",
	"https://ipv4.icanhazip.com",
	"https://checkip.amazonaws.com",
	"https://ifconfig.me/ip",
}

// egressProbeBudget caps the whole probe, however many services it has to
// walk. It has to stay under the 2s RefreshInterval times the streak
// threshold, or a slow probe would itself look like a state change.
const egressProbeBudget = 5 * time.Second

// fetchExternalIP returns the current egress address, trying each echo
// service until one answers. The error it reports on total failure names
// every service tried, because "no answer from api.ipify.org" in a log is
// misleading when three other services also failed — that pattern means the
// tunnel is down, not that one host is slow.
func fetchExternalIP() (string, error) {
	deadline := time.Now().Add(egressProbeBudget)
	var firstErr error
	tried := 0
	for _, svc := range echoServices {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		// Per-service slice of the budget: long enough to be a fair try,
		// short enough to leave room for the next service.
		perTry := 2500 * time.Millisecond
		if remaining < perTry {
			perTry = remaining
		}
		tried++
		ip, err := fetchIPFrom(svc, perTry)
		if err == nil {
			return ip, nil
		}
		if firstErr == nil {
			firstErr = fmt.Errorf("%s: %w", svc, err)
		}
	}
	if firstErr == nil {
		firstErr = fmt.Errorf("нет доступных сервисов проверки ip")
	}
	return "", fmt.Errorf("ни один из %d сервисов не ответил (%w)", tried, firstErr)
}

func fetchIPFrom(url string, timeout time.Duration) (string, error) {
	resp, err := egressHTTPClient(timeout).Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("http %d", resp.StatusCode)
	}
	// Bounded read: an echo service answers with ~40 bytes, and a captive
	// portal answering with an HTML page must not be slurped into memory.
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 128))
	ip := strings.TrimSpace(string(b))
	if net.ParseIP(ip) == nil {
		return "", fmt.Errorf("bad ip response: %q", ip)
	}
	return ip, nil
}
