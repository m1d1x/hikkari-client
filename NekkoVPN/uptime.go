package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// PingSample is one probe of one server. Stored as a compact array per profile.
type PingSample struct {
	Ts    int64 `json:"ts"`     // unix seconds
	OK    bool  `json:"ok"`
	RttMs int   `json:"rtt_ms"` // 0 if !OK
}

const (
	pingInterval  = 1 * time.Second
	pingTimeoutMs = 900
	historyMax    = 300 // 5 minutes at 1-second cadence
)

func (a *App) uptimePath() string { return filepath.Join(a.baseDir, "uptime-history.json") }

func (a *App) loadUptime() {
	a.upMu.Lock()
	defer a.upMu.Unlock()
	a.uptimeHist = map[string][]PingSample{}
	raw, err := os.ReadFile(a.uptimePath())
	if err != nil {
		return
	}
	var m map[string][]PingSample
	if err := json.Unmarshal(raw, &m); err == nil {
		a.uptimeHist = m
	}
}

func (a *App) saveUptime() {
	a.upMu.RLock()
	raw, err := json.MarshalIndent(a.uptimeHist, "", "  ")
	a.upMu.RUnlock()
	if err != nil {
		return
	}
	_ = os.WriteFile(a.uptimePath(), raw, 0644)
}

// GetUptimeHistory returns a deep-copied snapshot for the UI.
func (a *App) GetUptimeHistory() map[string][]PingSample {
	a.upMu.RLock()
	defer a.upMu.RUnlock()
	out := make(map[string][]PingSample, len(a.uptimeHist))
	for k, v := range a.uptimeHist {
		cp := make([]PingSample, len(v))
		copy(cp, v)
		out[k] = cp
	}
	return out
}

func (a *App) pingLoop() {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	a.pingAll()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			a.pingAll()
		}
	}
}

func (a *App) pingAll() {
	pf, err := a.readProfiles()
	if err != nil {
		return
	}
	now := time.Now().Unix()
	for _, p := range pf.Profiles {
		host := peerHost(p.Endpoint)
		if host == "" {
			continue
		}
		ok, rtt := pingHost(host)
		a.upMu.Lock()
		h := a.uptimeHist[p.Name]
		h = append(h, PingSample{Ts: now, OK: ok, RttMs: rtt})
		if len(h) > historyMax {
			h = h[len(h)-historyMax:]
		}
		a.uptimeHist[p.Name] = h
		a.upMu.Unlock()
	}
	a.saveUptime()
}

// peerHost pulls peer[0].address out of an endpoint map (the AWG server IP).
func peerHost(ep map[string]interface{}) string {
	peers, ok := ep["peers"].([]interface{})
	if !ok || len(peers) == 0 {
		return ""
	}
	p0, ok := peers[0].(map[string]interface{})
	if !ok {
		return ""
	}
	if s, _ := p0["address"].(string); s != "" {
		return s
	}
	return ""
}

var rttRe = regexp.MustCompile(`(?i)time[=<]\s*(\d+)\s*ms`)

func pingHost(host string) (bool, int) {
	cmd := exec.Command("ping", "-n", "1", "-w", strconv.Itoa(pingTimeoutMs), host)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	s := string(out)
	low := strings.ToLower(s)
	if strings.Contains(low, "request timed out") ||
		strings.Contains(low, "could not find host") ||
		strings.Contains(low, "destination host unreachable") {
		return false, 0
	}
	if err != nil {
		return false, 0
	}
	m := rttRe.FindStringSubmatch(s)
	if m == nil {
		return false, 0
	}
	rtt, _ := strconv.Atoi(m[1])
	return true, rtt
}
