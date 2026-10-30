// Package uptime maintains a rolling per-profile history of probe samples.
// The actual probe (engine adapter delay through the proxy when the engine is up,
// TCP-connect fallback when it isn't) is injected via the Probe interface
// so the tracker stays decoupled from native engine specifics.
package uptime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Sample is one probe of one profile. Stored as a compact JSON array per
// profile in uptime-history.json.
type Sample struct {
	Ts    int64  `json:"ts"`
	OK    bool   `json:"ok"`
	RttMs int    `json:"rtt_ms"`
	Src   Source `json:"src,omitempty"`
}

// Source says how a sample was measured. The signals are not equally
// strong: only SrcTunnel proves traffic actually passes through the proxy,
// SrcTCP proves the port accepts a handshake (DPI may still kill the
// protocol behind it), SrcICMP only proves the host answers pings. The
// downdetector relies on this distinction — an "ISP blocks it" verdict is
// only meaningful from tunnel samples. Empty on samples recorded before the
// field existed.
type Source string

const (
	SrcTunnel Source = "tunnel"
	SrcTCP    Source = "tcp"
	SrcICMP   Source = "icmp"
)

const (
	// PingInterval is the *per-profile* window — every profile is probed
	// once inside it. Each probe is a fresh connection through the tunnel
	// (engine adapter delay), so it competes with user traffic for the same
	// transport; 30s keeps the graph useful at a sixth of the old load.
	PingInterval = 30 * time.Second
	// PingTimeoutMs is the per-probe deadline. engine adapter uses this for the
	// HTTP delay request; the TCP fallback uses the same value.
	PingTimeoutMs = 2000
	// HistoryMax is the rolling window length (≈2.5 h at 30s intervals).
	HistoryMax = 300
)

// HostProvider yields the current "profile -> probe target" set. Called
// every tick so additions / removals are picked up without restarting the
// loop.
type HostProvider func() []HostEntry

// HostEntry pairs a profile name with everything the Probe needs to reach
// it. ProfileID drives the engine probe (when the engine is up); Host + Port
// drive the TCP fallback (when it isn't).
type HostEntry struct {
	Profile   string // human name (key in history map)
	ProfileID string // opaque profile ID, independent of native tags
	Host      string // server IP / hostname
	Port      int    // server port (TCP fallback target)
}

// Probe is the abstraction over the live/fallback probe pair. Implementations
// must be safe to call concurrently.
type Probe interface {
	// Run measures one sample. Returns (true, rttMs) on success, (false, 0)
	// on any failure (timeout, DPI block, engine off + UDP-only port). src
	// is the method that produced the result; on failure, the last method
	// tried.
	Run(ctx context.Context, h HostEntry) (ok bool, rttMs int, src Source)
}

// Tracker is the goroutine-safe in-memory history persisted to JSON on disk.
type Tracker struct {
	path string

	mu           sync.RWMutex
	history      map[string][]Sample
	scopeVersion int
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
	t.scopeVersion = 0
	raw, err := os.ReadFile(t.path)
	if err != nil {
		return
	}
	var versioned struct {
		Version int                 `json:"version"`
		History map[string][]Sample `json:"history"`
	}
	if json.Unmarshal(raw, &versioned) == nil && versioned.Version > 0 && versioned.History != nil {
		t.scopeVersion = versioned.Version
		t.history = versioned.History
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
	var value interface{} = t.history
	if t.scopeVersion > 0 {
		value = struct {
			Version int                 `json:"version"`
			History map[string][]Sample `json:"history"`
		}{t.scopeVersion, t.history}
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	t.mu.RUnlock()
	if err != nil {
		return
	}
	f, err := os.CreateTemp(filepath.Dir(t.path), ".uptime-*")
	if err != nil {
		return
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(raw); err != nil {
		_ = f.Close()
		return
	}
	if err = f.Close(); err != nil {
		return
	}
	_ = os.Rename(name, t.path)
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
			ok, rtt, src := probe.Run(ctx, h)
			t.record(h.Profile, ok, rtt, src)
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
func (t *Tracker) Record(key string, ok bool, rttMs int, src Source) {
	t.record(key, ok, rttMs, src)
}

func (t *Tracker) record(profile string, ok bool, rtt int, src Source) {
	now := time.Now().Unix()
	t.mu.Lock()
	defer t.mu.Unlock()
	hist := t.history[profile]
	hist = append(hist, Sample{Ts: now, OK: ok, RttMs: rtt, Src: src})
	if len(hist) > HistoryMax {
		hist = hist[len(hist)-HistoryMax:]
	}
	t.history[profile] = hist
}

// ServiceTarget describes one service probed in the background via engine adapter.
type ServiceTarget struct {
	Name string // e.g. "YouTube"
	URL  string // e.g. "https://youtube.com/generate_204"
}

// ActiveProfileProvider returns the current active profile ID. If no profile is active or VPN is off, returns "".
type ActiveProfileProvider func() string

// ServiceProbe measures one service target through a given proxy tag.
type ServiceProbe interface {
	Delay(tag, url string, timeoutMs int) (ms int, err error)
}

// RunServices is a blocking background loop that probes each service
// through the active profile's engine adapter delay endpoint. One round costs a
// new tunnel connection per service (8 by default), so the background
// cadence is slow; the UI's explicit ping (Core.PingServices) covers the
// "check now" case.
const ServicePingInterval = 5 * time.Minute

// serviceFirstPing is the delay before the first background round, so the
// history is not empty for the first ServicePingInterval after launch.
const serviceFirstPing = 30 * time.Second

// ServiceTargetProvider returns the current list of service targets to ping.
// Called every tick so additions/removals are picked up without restart.
type ServiceTargetProvider func() []ServiceTarget

func (t *Tracker) RunServices(ctx context.Context, activeTag ActiveProfileProvider, targets ServiceTargetProvider, probe ServiceProbe) {
	wait := serviceFirstPing
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = ServicePingInterval
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
					t.record(tag+"|"+svc.Name, false, 0, SrcTunnel)
				} else {
					t.record(tag+"|"+svc.Name, true, ms, SrcTunnel)
				}
			}(svc)
		}
		wg.Wait()
		t.Save()
	}
}

// MigrateScopes converts a legacy service-history file in one pass. Versioning
// prevents a profile literally named "p-server" from being mistaken for the old
// native tag of "server" after the next application restart.
func (t *Tracker) MigrateScopes(scopes map[string]string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.scopeVersion >= 1 {
		return
	}
	migrated := make(map[string][]Sample, len(t.history))
	for key, samples := range t.history {
		scope, service, ok := strings.Cut(key, "|")
		if replacement, exists := scopes[scope]; ok && exists {
			key = replacement + "|" + service
		}
		migrated[key] = append(migrated[key], samples...)
	}
	for key, samples := range migrated {
		sort.SliceStable(samples, func(i, j int) bool { return samples[i].Ts < samples[j].Ts })
		migrated[key] = samples
	}
	t.history = migrated
	t.scopeVersion = 1
}
