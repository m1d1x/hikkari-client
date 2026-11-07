// Package uptime maintains a rolling per-profile history of probe samples.
// The actual probe (engine adapter delay through the proxy when the engine is up,
// TCP-connect fallback when it isn't) is injected via the Probe interface
// so the tracker stays decoupled from native engine specifics.
package uptime

import (
	"NekkoVPN/core/fsutil"
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
	SrcProber Source = "prober" // UI projection only; never added to the local tracker.
	SrcTunnel Source = "tunnel"
	SrcTCP    Source = "tcp"
	SrcICMP   Source = "icmp"
)

const (
	// PingInterval is how often the whole list is re-probed while the server
	// list is on screen. Nobody looks at those numbers otherwise, so nothing
	// measures the other servers once it closes.
	PingInterval = 30 * time.Second
	// sweepMinGap keeps a user flicking the list open and shut from turning
	// into a stream of full sweeps.
	sweepMinGap = 10 * time.Second
	// sweepConcurrency bounds one sweep; each probe opens its own connection.
	sweepConcurrency = 8
	// ActiveCheckInterval is how often the profile carrying traffic is looked
	// at while the list is closed.
	ActiveCheckInterval = 120 * time.Second
	// trafficWindow is how long a check watches for bytes before it considers
	// a probe. Bytes arriving through the profile prove it better than a
	// synthetic request, which would compete with them for the same transport.
	trafficWindow = 10 * time.Second
	// PingTimeoutMs is the per-probe deadline. engine adapter uses this for the
	// HTTP delay request; the TCP fallback uses the same value.
	PingTimeoutMs = 2000
	// HistoryMax is the rolling window length (10 h of active checks).
	HistoryMax = 300
)

// HostProvider yields the current "profile -> probe target" set. Called
// before every sweep so additions / removals are picked up without restarting
// the loop.
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

// ActiveProvider names the profile carrying traffic right now: "" while the
// engine is off, which is when nothing runs in the background at all.
type ActiveProvider func() string

// Observer reads what the engine already knows, at no network cost. nil when
// the engine has no such view; every check then ends in a probe.
type Observer interface {
	// Received is the bytes received so far per open connection through the
	// profile.
	Received(profileID string) (map[string]uint64, error)
	// OwnProbe is the engine's last own measurement of the profile. known is
	// false unless the engine is probing it anyway (auto mode).
	OwnProbe(profileID string) (ok bool, rttMs int, known bool)
}

// Tracker is the goroutine-safe in-memory history persisted to JSON on disk.
type Tracker struct {
	path string

	mu           sync.RWMutex
	history      map[string][]Sample
	scopeVersion int
	listOpen     bool
	wake         chan struct{}
}

func New(path string) *Tracker {
	return &Tracker{path: path, history: map[string][]Sample{}, wake: make(chan struct{}, 1)}
}

// SetUIPresence tells the tracker whether the server list is on screen. Opening
// it starts a sweep at once; closing it stops the next probe from starting.
func (t *Tracker) SetUIPresence(open bool) {
	t.mu.Lock()
	t.listOpen = open
	t.mu.Unlock()
	t.Nudge()
}

// Nudge re-evaluates the loop now rather than at its next timer: the list opened
// or closed, or the profile carrying traffic changed.
func (t *Tracker) Nudge() {
	select {
	case t.wake <- struct{}{}:
	default:
	}
}

func (t *Tracker) presence() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.listOpen
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
	_ = fsutil.Replace(name, t.path)
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

// Run is a blocking loop that exits when ctx is canceled. It only spends network
// on what someone can see or on the profile in use:
//
//   - list open: every profile, once per PingInterval;
//   - list closed: the active profile, once per ActiveCheckInterval, and not even
//     that while bytes are flowing through it;
//   - engine off, list closed: nothing.
//
// Failover discovery is not here: the core re-probes the pool itself the moment
// its held node fails.
func (t *Tracker) Run(ctx context.Context, hosts HostProvider, active ActiveProvider, probe Probe, observer Observer) {
	var lastSweep, lastCheck time.Time
	lastActive := ""
	for ctx.Err() == nil {
		var wait time.Duration
		if t.presence() {
			if gap := time.Since(lastSweep); !lastSweep.IsZero() && gap < sweepMinGap {
				wait = sweepMinGap - gap
			} else {
				t.sweep(ctx, hosts(), probe)
				lastSweep = time.Now()
				wait = PingInterval
			}
		} else {
			current := active()
			due := time.Since(lastCheck) >= ActiveCheckInterval
			if current != "" && (due || current != lastActive) {
				if host, ok := findHost(hosts(), current); ok {
					t.checkActive(ctx, host, probe, observer)
				}
				lastCheck = time.Now()
			}
			lastActive = current
			wait = ActiveCheckInterval
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
		case <-timer.C:
		case <-t.wake:
		}
		timer.Stop()
	}
}

// sweep probes the given profiles with bounded concurrency, stopping as soon as
// the list closes. A result landing after the close is still a true measurement,
// so it is kept.
func (t *Tracker) sweep(ctx context.Context, entries []HostEntry, probe Probe) {
	slots := make(chan struct{}, sweepConcurrency)
	var wg sync.WaitGroup
	for _, h := range entries {
		if h.Profile == "" {
			continue
		}
		select {
		case <-ctx.Done():
		case slots <- struct{}{}:
		}
		if ctx.Err() != nil || !t.presence() {
			break
		}
		wg.Add(1)
		go func(h HostEntry) {
			defer wg.Done()
			defer func() { <-slots }()
			ok, rtt, src := probe.Run(ctx, h)
			t.record(h.Profile, ok, rtt, src)
		}(h)
	}
	wg.Wait()
	t.Save()
}

// checkActive produces at most one sample for the profile in use, in order of
// cost: bytes flowing (a success at the last delay measured, no network), the
// engine's own probe (no network), then one real probe. A link carrying
// traffic still leaves a sample: the downdetector reports from this history,
// and a profile that works for hours would otherwise look unreported.
func (t *Tracker) checkActive(ctx context.Context, h HostEntry, probe Probe, observer Observer) {
	if observer != nil {
		flowing, stop := t.watchTraffic(ctx, observer, h.ProfileID)
		if stop {
			return
		}
		if flowing {
			if rtt, ok := t.lastRtt(h.Profile); ok {
				t.record(h.Profile, true, rtt, SrcTunnel)
				t.Save()
				return
			}
		}
		if ok, rtt, known := observer.OwnProbe(h.ProfileID); known {
			t.record(h.Profile, ok, rtt, SrcTunnel)
			t.Save()
			return
		}
	}
	ok, rtt, src := probe.Run(ctx, h)
	t.record(h.Profile, ok, rtt, src)
	t.Save()
}

// watchTraffic watches the profile for trafficWindow. flowing: some connection
// through it received bytes. stop: something more urgent came up and the check
// ends without a sample; the list opening must not wait out the window, so a
// wake is handed back to Run. Read errors count as a quiet link: the caller
// then measures, as it would anyway.
func (t *Tracker) watchTraffic(ctx context.Context, observer Observer, profileID string) (flowing, stop bool) {
	before, err := observer.Received(profileID)
	if err != nil {
		return false, false
	}
	timer := time.NewTimer(trafficWindow)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false, true
	case <-t.wake:
		t.Nudge()
		return false, true
	case <-timer.C:
	}
	after, err := observer.Received(profileID)
	if err != nil {
		return false, false
	}
	for id, received := range after {
		if received > before[id] {
			return true, false
		}
	}
	return false, false
}

// lastRtt is the delay of the profile's latest successful sample.
func (t *Tracker) lastRtt(profile string) (int, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	hist := t.history[profile]
	for i := len(hist) - 1; i >= 0; i-- {
		if hist[i].OK {
			return hist[i].RttMs, true
		}
	}
	return 0, false
}

func findHost(entries []HostEntry, profileID string) (HostEntry, bool) {
	for _, h := range entries {
		if h.ProfileID == profileID {
			return h, true
		}
	}
	return HostEntry{}, false
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

// ServiceTarget describes one service the user can ping on demand.
type ServiceTarget struct {
	Name string // e.g. "YouTube"
	URL  string // e.g. "https://youtube.com/generate_204"
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
