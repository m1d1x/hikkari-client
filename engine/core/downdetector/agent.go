package downdetector

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"sync"
	"time"

	"NekkoVPN/core/uptime"
)

const (
	// InstallTTL is how long one install id lives. Rotating it caps how
	// long reports from one device can be linked to each other.
	InstallTTL = 7 * 24 * time.Hour
	// OutboxMax bounds the local queue to ~1h of reports; older ones are
	// dropped rather than piling up while the server is unreachable.
	OutboxMax = 12
	// settleDelay lets the last probe of a window land before it is built.
	settleDelay = 5 * time.Second
)

// Settings are the user-facing switches.
type Settings struct {
	Enabled      bool `json:"enabled"`
	SharePrivate bool `json:"share_private"`
}

type state struct {
	Settings
	Install      string `json:"install"`
	InstallSince int64  `json:"install_since"`
}

// Deps is how the agent reads the rest of the engine.
type Deps struct {
	Servers func() []Server
	History func() map[string][]uptime.Sample
	Net     func() Net
}

// Agent builds a report every Window and queues it in a local outbox.
type Agent struct {
	statePath  string
	outboxPath string
	deps       Deps

	mu     sync.Mutex
	st     state
	outbox []Report
	last   *Report
}

// New returns an agent with default settings: enabled, private servers not
// shared. Call Load to pick up what is on disk.
func New(statePath, outboxPath string, d Deps) *Agent {
	return &Agent{
		statePath:  statePath,
		outboxPath: outboxPath,
		deps:       d,
		st:         state{Settings: Settings{Enabled: true}},
	}
}

// Load reads settings and the queued outbox. Missing or malformed files
// are tolerated — the defaults stay.
func (a *Agent) Load() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if raw, err := os.ReadFile(a.statePath); err == nil {
		var st state
		if json.Unmarshal(raw, &st) == nil {
			a.st = st
		}
	}
	if raw, err := os.ReadFile(a.outboxPath); err == nil {
		var ob []Report
		if json.Unmarshal(raw, &ob) == nil {
			a.outbox = ob
			if n := len(ob); n > 0 {
				a.last = &ob[n-1]
			}
		}
	}
}

func (a *Agent) Settings() Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.st.Settings
}

// SetSettings persists s. Turning the agent off also drops everything
// still queued: "off" means nothing leaves, including the backlog.
func (a *Agent) SetSettings(s Settings) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.st.Settings = s
	if !s.Enabled {
		a.outbox = nil
		a.last = nil
		if err := a.saveOutboxLocked(); err != nil {
			return err
		}
	}
	return a.saveStateLocked()
}

// LastReport is the most recent report exactly as it would be sent, for
// the "what is sent" screen. nil if none yet.
func (a *Agent) LastReport() *Report {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.last == nil {
		return nil
	}
	cp := *a.last
	cp.Servers = append([]ServerStat(nil), a.last.Servers...)
	return &cp
}

// Outbox returns a copy of the queued reports.
func (a *Agent) Outbox() []Report {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]Report(nil), a.outbox...)
}

// Run builds one report per Window, right after each window closes. Blocks
// until ctx is done.
func (a *Agent) Run(ctx context.Context) {
	for {
		now := time.Now()
		next := now.Truncate(Window).Add(Window)
		select {
		case <-ctx.Done():
			return
		case <-time.After(next.Sub(now) + settleDelay):
		}
		a.tick(next.Add(-Window), time.Now())
	}
}

// tick builds and queues the report for the window starting at start.
func (a *Agent) tick(start, now time.Time) {
	a.mu.Lock()
	enabled, share := a.st.Enabled, a.st.SharePrivate
	a.mu.Unlock()
	if !enabled {
		return
	}

	var servers []Server
	for _, s := range a.deps.Servers() {
		if s.Private && !share {
			continue
		}
		servers = append(servers, s)
	}
	if len(servers) == 0 {
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.rotateLocked(now)
	r := Build(a.st.Install, a.deps.Net(), start, servers, a.deps.History())
	if len(r.Servers) == 0 {
		return
	}
	a.outbox = append(a.outbox, r)
	if len(a.outbox) > OutboxMax {
		a.outbox = a.outbox[len(a.outbox)-OutboxMax:]
	}
	a.last = &a.outbox[len(a.outbox)-1]
	_ = a.saveOutboxLocked()
	_ = a.saveStateLocked()
}

func (a *Agent) rotateLocked(now time.Time) {
	if a.st.Install != "" && now.Sub(time.Unix(a.st.InstallSince, 0)) < InstallTTL {
		return
	}
	var b [12]byte
	_, _ = rand.Read(b[:])
	a.st.Install = "r-" + hex.EncodeToString(b[:])
	a.st.InstallSince = now.Unix()
}

func (a *Agent) saveStateLocked() error {
	raw, err := json.MarshalIndent(a.st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(a.statePath, raw, 0644)
}

func (a *Agent) saveOutboxLocked() error {
	raw, err := json.MarshalIndent(a.outbox, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(a.outboxPath, raw, 0644)
}
