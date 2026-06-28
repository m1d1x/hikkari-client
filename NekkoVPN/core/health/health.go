// Package health classifies the tunnel state into one of four canonical
// statuses the UI consumes: "off", "connecting", "ok", "broken". It also
// maintains a baseline ISP IP captured while VPN was provably OFF — that
// baseline is how we know later that the tunnel "leaked" (egress == ISP).
package health

import (
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

// Checker is the stateful health probe.
type Checker struct {
	directIPPath string
	log          Logger
	vpnHosts     VPNHostProvider

	mu          sync.Mutex
	directIP    string // last-known IP captured while VPN was provably OFF
	lastIP      string
	lastStatus  string
	lastChecked time.Time
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

// ExternalIP returns the current egress IP, or "—" on failure. Convenience
// wrapper for the UI.
func (c *Checker) ExternalIP() string {
	ip, _ := fetchExternalIP()
	if ip == "" {
		return "—"
	}
	return ip
}

// Probe performs one health check and returns the canonical Info.
//
//	core down              → "off"
//	core up, no egress yet → "connecting"
//	core up, egress == ISP → "broken"
//	core up, egress != ISP → "ok"
func (c *Checker) Probe() Info {
	hi := Info{}
	hi.Running = vpn.ClashAPIRunning()

	if !hi.Running {
		hi.Status = "off"
		hi.Message = "ядро не запущено"
		if ip, err := fetchExternalIP(); err == nil {
			hi.IP = ip
			// Guard: never let a VPN-server IP become the "ISP baseline".
			// During the shutdown window TUN may still route briefly even
			// though Clash API stopped answering — without the guard, the
			// next Probe() would see egress == baseline and flip the UI
			// to "broken" forever.
			if c.isOurVPNServer(ip) {
				c.log.Logf("health: refusing to set baseline ISP IP to known VPN server %q", ip)
			} else {
				c.mu.Lock()
				if c.directIP != ip {
					c.log.Logf("health: baseline ISP IP updated %q -> %q", c.directIP, ip)
					c.directIP = ip
					c.saveDirectIP(ip)
				}
				c.mu.Unlock()
			}
		}
		c.transition("off", hi.IP, hi.Message)
		return hi
	}

	ip, err := fetchExternalIP()
	if err != nil || ip == "" {
		hi.Status = "connecting"
		hi.Message = "проверяю выход в сеть"
		if err != nil {
			hi.Message = fmt.Sprintf("нет ответа от api.ipify.org: %v", err)
		}
		c.transition("connecting", "", hi.Message)
		return hi
	}
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
	c.transition("ok", ip, "")
	return hi
}

func (c *Checker) isOurVPNServer(ip string) bool {
	if c.vpnHosts == nil {
		return false
	}
	for _, h := range c.vpnHosts() {
		if h == ip {
			return true
		}
	}
	return false
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

// freshHTTPClient returns a client whose every request opens a brand-new
// TCP connection. Critical for the egress probe: if we reused a connection
// established before sing-box took over routing, ipify would keep replying
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

func fetchExternalIP() (string, error) {
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
