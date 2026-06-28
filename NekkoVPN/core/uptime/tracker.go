// Package uptime maintains a rolling per-profile history of ping samples.
// The ping primitive itself is platform-split (ping_windows.go vs ping_other.go)
// so the package compiles cleanly under gomobile for Android/iOS later.
package uptime

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"time"
)

// Sample is one probe of one server. Stored as a compact JSON array per profile.
type Sample struct {
	Ts    int64 `json:"ts"`
	OK    bool  `json:"ok"`
	RttMs int   `json:"rtt_ms"`
}

const (
	// PingInterval is how often each known profile is probed.
	PingInterval = 1 * time.Second
	// PingTimeoutMs is the per-probe deadline.
	PingTimeoutMs = 900
	// HistoryMax is the rolling window length (≈5 min at 1Hz).
	HistoryMax = 300
)

// HostProvider yields the list of "profile name -> host" pairs to probe.
// The tracker calls it every tick — passing it in lets us avoid coupling
// uptime to the profile package.
type HostProvider func() []HostEntry

// HostEntry pairs a profile name with the host to ping for it.
type HostEntry struct {
	Profile string
	Host    string
}

// Tracker is a goroutine-safe in-memory history persisted to JSON on disk.
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

// Forget drops the history for a single profile (used when a profile is deleted).
func (t *Tracker) Forget(profile string) {
	t.mu.Lock()
	delete(t.history, profile)
	t.mu.Unlock()
}

// Run is a blocking ping loop. It exits when ctx is canceled. Pass it the
// HostProvider that returns the current set of "name -> host" pairs.
func (t *Tracker) Run(ctx context.Context, hosts HostProvider) {
	ticker := time.NewTicker(PingInterval)
	defer ticker.Stop()
	t.tick(hosts)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			t.tick(hosts)
		}
	}
}

func (t *Tracker) tick(hosts HostProvider) {
	now := time.Now().Unix()
	for _, h := range hosts() {
		if h.Host == "" {
			continue
		}
		ok, rtt := pingHost(h.Host)
		t.mu.Lock()
		hist := t.history[h.Profile]
		hist = append(hist, Sample{Ts: now, OK: ok, RttMs: rtt})
		if len(hist) > HistoryMax {
			hist = hist[len(hist)-HistoryMax:]
		}
		t.history[h.Profile] = hist
		t.mu.Unlock()
	}
	t.Save()
}
