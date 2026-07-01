// Package uptime maintains a rolling per-profile history of probe samples.
// The actual probe (Clash API delay through the proxy when the engine is up,
// TCP-connect fallback when it isn't) is injected via the Probe interface
// so the tracker stays decoupled from sing-box specifics.
package uptime

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"time"
)

// Sample is one probe of one profile. Stored as a compact JSON array per
// profile in uptime-history.json.
type Sample struct {
	Ts    int64 `json:"ts"`
	OK    bool  `json:"ok"`
	RttMs int   `json:"rtt_ms"`
}

const (
	// PingInterval is the *per-profile* window — every profile is probed
	// once inside it. During development we keep this short (5s) so the
	// graphs are visibly responsive; for shipped builds it should be bumped
	// to 30-60s to be polite to the network.
	PingInterval = 5 * time.Second
	// PingTimeoutMs is the per-probe deadline. Clash API uses this for the
	// HTTP delay request; the TCP fallback uses the same value.
	PingTimeoutMs = 2000
	// HistoryMax is the rolling window length (≈25 min at 5s intervals).
	HistoryMax = 300
)

// HostProvider yields the current "profile -> probe target" set. Called
// every tick so additions / removals are picked up without restarting the
// loop.
type HostProvider func() []HostEntry

// HostEntry pairs a profile name with everything the Probe needs to reach
// it. Tag drives the Clash-API path (when the engine is up); Host + Port
// drive the TCP fallback (when it isn't).
type HostEntry struct {
	Profile string // human name (key in history map)
	Tag     string // sing-box outbound/endpoint tag, e.g. "p-main"
	Host    string // server IP / hostname
	Port    int    // server port (TCP fallback target)
}

// Probe is the abstraction over the live/fallback probe pair. Implementations
// must be safe to call concurrently.
type Probe interface {
	// Run measures one sample. Returns (true, rttMs) on success, (false, 0)
	// on any failure (timeout, DPI block, engine off + UDP-only port).
	Run(ctx context.Context, h HostEntry) (ok bool, rttMs int)
}

// Tracker is the goroutine-safe in-memory history persisted to JSON on disk.
type Tracker struct {
	path string

	mu      sync.RWMutex
	history map[string][]Sample
}

func New(path string) *Tracker {
	return &Tracker{path: path, history: map[string][]Sample{}}
}

// Load replaces the in-memory history with whatever is on disk. Missing or
// malformed files are tolerated silently — they just stay empty.
func (t *Tracker) Load() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.history = map[string][]Sample{}
	raw, err := os.ReadFile(t.path)
	if err != nil {
		return
	}
	var m map[string][]Sample
	if err := json.Unmarshal(raw, &m); err == nil {
		t.history = m
	}
}

// Save writes the current history to disk.
func (t *Tracker) Save() {
	t.mu.RLock()
	raw, err := json.MarshalIndent(t.history, "", "  ")
	t.mu.RUnlock()
	if err != nil {
		return
	}
	_ = os.WriteFile(t.path, raw, 0644)
}

// Snapshot returns a deep copy of the history for handing off to the UI.
func (t *Tracker) Snapshot() map[string][]Sample {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[string][]Sample, len(t.history))
	for k, v := range t.history {
		cp := make([]Sample, len(v))
		copy(cp, v)
		out[k] = cp
	}
	return out
}

// Forget drops the history for a single profile (used when a profile is
// deleted).
func (t *Tracker) Forget(profile string) {
	t.mu.Lock()
	delete(t.history, profile)
	t.mu.Unlock()
}

// Run is a blocking probe loop. It exits when ctx is canceled. Probes are
// staggered *within* a PingInterval window so the network sees one
// outgoing request every interval/N seconds rather than N simultaneous
// bursts — important when several profiles share a server or the user
// is on a tight connection.
func (t *Tracker) Run(ctx context.Context, hosts HostProvider, probe Probe) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		t.runWindow(ctx, hosts, probe)
	}
}

// runWindow probes each profile exactly once over PingInterval, evenly
// spaced. If a window slot finds no profiles (none configured yet) we
// sleep through the whole window and try again next round.
func (t *Tracker) runWindow(ctx context.Context, hosts HostProvider, probe Probe) {
	entries := hosts()
	if len(entries) == 0 {
		select {
		case <-ctx.Done():
		case <-time.After(PingInterval):
		}
		return
	}

	windowStart := time.Now()
	step := PingInterval / time.Duration(len(entries))

	var wg sync.WaitGroup
	for i, h := range entries {
		if h.Profile == "" {
			continue
		}
		// Sleep until this probe's slot inside the current window.
		target := windowStart.Add(step * time.Duration(i))
		if d := time.Until(target); d > 0 {
			select {
			case <-ctx.Done():
				wg.Wait()
				return
			case <-time.After(d):
			}
		}
		wg.Add(1)
		go func(h HostEntry) {
			defer wg.Done()
			ok, rtt := probe.Run(ctx, h)
			t.record(h.Profile, ok, rtt)
		}(h)
	}
	// Make sure we don't shorten the window if the last slot's probe is
	// still in flight — keeps the rhythm steady.
	wg.Wait()
	if rem := time.Until(windowStart.Add(PingInterval)); rem > 0 {
		select {
		case <-ctx.Done():
		case <-time.After(rem):
		}
	}
	t.Save()
}

// Record appends a single sample under key. Exported wrapper over record()
// for on-demand callers outside this package (e.g. the service-ping path in
// core/api). Persist with Save() when done.
func (t *Tracker) Record(key string, ok bool, rttMs int) {
	t.record(key, ok, rttMs)
}

func (t *Tracker) record(profile string, ok bool, rtt int) {
	now := time.Now().Unix()
	t.mu.Lock()
	defer t.mu.Unlock()
	hist := t.history[profile]
	hist = append(hist, Sample{Ts: now, OK: ok, RttMs: rtt})
	if len(hist) > HistoryMax {
		hist = hist[len(hist)-HistoryMax:]
	}
	t.history[profile] = hist
}

// ServiceTarget describes one service probed in the background via Clash API.
type ServiceTarget struct {
	Name string // e.g. "YouTube"
	URL  string // e.g. "https://youtube.com/generate_204"
}

// ActiveTagProvider returns the current active profile's sing-box tag (e.g.
// "p-main"). If no profile is active or VPN is off, returns "".
type ActiveTagProvider func() string

// ServiceProbe measures one service target through a given proxy tag.
type ServiceProbe interface {
	Delay(tag, url string, timeoutMs int) (ms int, err error)
}

// RunServices is a blocking background loop that probes each service
// through the active profile's Clash API delay endpoint. The UI never
// triggers pings — it only reads the history this loop writes.
const ServicePingInterval = 10 * time.Second

// ServiceTargetProvider returns the current list of service targets to ping.
// Called every tick so additions/removals are picked up without restart.
type ServiceTargetProvider func() []ServiceTarget

func (t *Tracker) RunServices(ctx context.Context, activeTag ActiveTagProvider, targets ServiceTargetProvider, probe ServiceProbe) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(ServicePingInterval):
		}
		tag := activeTag()
		if tag == "" {
			continue
		}
		cur := targets()
		if len(cur) == 0 {
			continue
		}
		var wg sync.WaitGroup
		for _, svc := range cur {
			wg.Add(1)
			go func(svc ServiceTarget) {
				defer wg.Done()
				ms, err := probe.Delay(tag, svc.URL, PingTimeoutMs)
				if err != nil {
					t.record(tag+"|"+svc.Name, false, 0)
				} else {
					t.record(tag+"|"+svc.Name, true, ms)
				}
			}(svc)
		}
		wg.Wait()
		t.Save()
	}
}
