// Package autotunnel sends sites through the tunnel when they stay silent on
// the direct path. An app the user sent direct takes all its traffic with it,
// including the sites its provider blocks: a browser waits on them and retries,
// and the page either fails or loads slowly. Nobody can list those sites in
// advance, since they differ by provider and change, but the connections show
// them: a TLS hello went out and nothing ever came back.
//
// Sites go into a list the core reads as a rule set and reloads by itself, so
// the running tunnel and its connections are not touched. The list is kept per
// physical network and entries expire: a site unblocked later goes back to the
// direct path on its own.
package autotunnel

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"NekkoVPN/core/fsutil"
)

const (
	// silentAfter is how long a connection may wait for its first byte. A TLS
	// server answers a hello at once; a blocked one never does.
	silentAfter = 5 * time.Second
	// minHello is the least a connection must have sent to have asked at all.
	minHello = 200
	// silentConns is how many separate connections must stay silent: one can
	// die for any reason, and a browser retries a blocked site right away.
	silentConns = 2
	// silentWindow is how long the evidence for a host stays good.
	silentWindow = 2 * time.Minute
	// entryTTL is how long a site stays in the list before the direct path gets
	// another try.
	entryTTL = 7 * 24 * time.Hour
	// maxHosts bounds the list; the entry closest to expiry goes first.
	maxHosts = 256
	// pollInterval: the connection list is answered from the core's memory and
	// sends nothing over the network.
	pollInterval = 5 * time.Second
)

// Conn is one open connection as the core reports it.
type Conn struct {
	ID       string
	Host     string // the name asked for, "" when only an address is known
	Network  string
	Port     string
	Upload   uint64
	Download uint64
	Direct   bool
	Start    time.Time
}

// Detector picks out the hosts that stay silent on the direct path.
type Detector struct {
	seen map[string]*hostEvidence
}

type hostEvidence struct {
	silent   map[string]bool // connection IDs
	lastSeen time.Time
}

// NewDetector returns a detector with no evidence.
func NewDetector() *Detector { return &Detector{seen: map[string]*hostEvidence{}} }

// Observe takes the current connections and returns the hosts that just became
// proven silent.
func (d *Detector) Observe(now time.Time, conns []Conn) []string {
	for host, e := range d.seen {
		if now.Sub(e.lastSeen) > silentWindow {
			delete(d.seen, host)
		}
	}
	answered := map[string]bool{}
	for _, c := range conns {
		if !watched(c) {
			continue
		}
		if c.Download > 0 {
			answered[c.Host] = true
			continue
		}
		if c.Upload < minHello || now.Sub(c.Start) < silentAfter {
			continue
		}
		e := d.seen[c.Host]
		if e == nil {
			e = &hostEvidence{silent: map[string]bool{}}
			d.seen[c.Host] = e
		}
		e.silent[c.ID] = true
		e.lastSeen = now
	}
	var proven []string
	for host, e := range d.seen {
		// One answer proves the path works, whatever the other connections did.
		if answered[host] {
			delete(d.seen, host)
			continue
		}
		if len(e.silent) >= silentConns {
			proven = append(proven, host)
			delete(d.seen, host)
		}
	}
	slices.Sort(proven)
	return proven
}

// watched: TLS over TCP to a named host on the direct path. Only there does
// silence mean something: other ports may rightly wait for the client to speak,
// and without a name there is nothing to route by.
func watched(c Conn) bool {
	return c.Direct && c.Network == "tcp" && c.Port == "443" && isDomain(c.Host)
}

func isDomain(host string) bool {
	return strings.Contains(host, ".") && net.ParseIP(host) == nil
}

// List is the set of hosts, kept in two files: the rule set the core reads and
// the expiry times it does not need to know about.
type List struct {
	path string

	mu    sync.Mutex
	hosts map[string]time.Time
}

// OpenList reads the list behind the rule set at path. Damaged state only
// costs a new round of detection.
func OpenList(path string) *List {
	l := &List{path: path, hosts: map[string]time.Time{}}
	raw, err := os.ReadFile(l.statePath())
	if err == nil {
		_ = json.Unmarshal(raw, &l.hosts)
	}
	return l
}

func (l *List) statePath() string { return l.path + ".state" }

// Add records hosts.
func (l *List) Add(now time.Time, hosts []string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, h := range hosts {
		l.hosts[h] = now.Add(entryTTL)
	}
	for len(l.hosts) > maxHosts {
		var oldest string
		for h, at := range l.hosts {
			if oldest == "" || at.Before(l.hosts[oldest]) {
				oldest = h
			}
		}
		delete(l.hosts, oldest)
	}
	return l.saveLocked()
}

// Expire drops the entries past their time, writing only when there are any.
func (l *List) Expire(now time.Time) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	expired := false
	for h, at := range l.hosts {
		if now.After(at) {
			delete(l.hosts, h)
			expired = true
		}
	}
	if !expired {
		return nil
	}
	return l.saveLocked()
}

// Reset empties the list: what one network blocks says nothing about another.
func (l *List) Reset() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.hosts) == 0 {
		return nil
	}
	l.hosts = map[string]time.Time{}
	return l.saveLocked()
}

// Hosts lists the current entries, sorted.
func (l *List) Hosts() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sortedLocked()
}

func (l *List) sortedLocked() []string {
	out := make([]string, 0, len(l.hosts))
	for h := range l.hosts {
		out = append(out, h)
	}
	slices.Sort(out)
	return out
}

func (l *List) saveLocked() error {
	state, err := json.Marshal(l.hosts)
	if err != nil {
		return err
	}
	if err := writeAtomic(l.statePath(), state); err != nil {
		return err
	}
	content, err := RuleSet(l.sortedLocked())
	if err != nil {
		return err
	}
	return writeAtomic(l.path, content)
}

// RuleSet renders hosts as a sing-box source rule set. An empty set is valid:
// the core needs the file to exist from its first start.
func RuleSet(hosts []string) ([]byte, error) {
	rules := []map[string][]string{}
	if len(hosts) > 0 {
		rules = append(rules, map[string][]string{"domain": hosts})
	}
	return json.Marshal(map[string]interface{}{"version": 3, "rules": rules})
}

// EnsureRuleSet creates an empty rule set when there is none yet. The core
// refuses to start over a rule set file that is missing.
func EnsureRuleSet(path string) error {
	if _, err := os.Stat(path); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	content, err := RuleSet(nil)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeAtomic(path, content)
}

func writeAtomic(path string, content []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, content, 0o644); err != nil {
		return err
	}
	return fsutil.Replace(tmp, path)
}

// Run watches the connections until ctx is done. A failed read means the core
// is down, which is an ordinary state and costs nothing.
func Run(ctx context.Context, list *List, conns func() ([]Conn, error), logf func(string, ...interface{})) {
	detector := NewDetector()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		now := time.Now()
		if err := list.Expire(now); err != nil {
			logf("[autotunnel] expire: %v", err)
		}
		current, err := conns()
		if err != nil {
			continue
		}
		proven := detector.Observe(now, current)
		if len(proven) == 0 {
			continue
		}
		if err := list.Add(now, proven); err != nil {
			logf("[autotunnel] saving %d host(s): %v", len(proven), err)
			continue
		}
		logf("[autotunnel] silent on the direct path, now through the tunnel: %s (list=%d)", strings.Join(proven, " "), len(list.Hosts()))
	}
}
