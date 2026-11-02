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
	"fmt"
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

// Tag is the rule set tag of the autotunnel host list. It is the one local
// rule set a GUI-compiled config may declare: svcproto.Sanitize rewrites its
// path to the service-owned location, and the service owns the file the core
// reads — the GUI pushes the hosts as data (svcproto OpAutoTunnel), never a
// path.
const Tag = "auto-tunnel"

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
	// maxHostLen bounds one host: the longest domain name the core can match.
	maxHostLen = 253
)

// resyncInterval is how often Run re-publishes the whole list even without a
// change. The service owns the rule set file and can lose it — its directory
// replaced by an update — and a heartbeat heals that without waiting for new
// evidence. A var so tests can shorten it.
var resyncInterval = 5 * time.Minute

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
// the expiry times it does not need to know about. On desktop the rule set the
// running core follows is the service's copy (see SetPublish); the file here is
// the GUI's own, for an engine run directly from this config.
type List struct {
	path string

	mu    sync.Mutex
	hosts map[string]time.Time

	// publish, when set, receives the whole list after every change and from
	// Publish. It pushes the hosts to whoever runs the core with this list —
	// on desktop, the service that owns the rule set file.
	publish func([]string) error
	// logf hears publish failures. The local list is still saved: it is the
	// source of truth, and the next change or heartbeat retries.
	logf func(string, ...interface{})
}

// OpenList reads the list behind the rule set at path. Damaged state only
// costs a new round of detection. logf may be nil.
func OpenList(path string, logf func(string, ...interface{})) *List {
	l := &List{path: path, logf: logf, hosts: map[string]time.Time{}}
	raw, err := os.ReadFile(l.statePath())
	if err == nil {
		_ = json.Unmarshal(raw, &l.hosts)
	}
	return l
}

// SetPublish wires the push to the core's rule set. Called once, before Run.
func (l *List) SetPublish(f func([]string) error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.publish = f
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
	hosts := l.sortedLocked()
	if err := WriteRuleSet(l.path, hosts); err != nil {
		return err
	}
	if l.publish != nil {
		if err := l.publish(hosts); err != nil && l.logf != nil {
			l.logf("[autotunnel] publish: %v", err)
		}
	}
	return nil
}

// Publish pushes the whole current list to whoever follows it. Run calls it
// once at start and on every heartbeat; the GUI calls it before starting the
// core, so the service's rule set is never stale against a fresh start.
func (l *List) Publish() error {
	l.mu.Lock()
	hook, hosts := l.publish, l.sortedLocked()
	l.mu.Unlock()
	if hook == nil {
		return nil
	}
	return hook(hosts)
}

// ValidateHosts reports whether hosts are a list the core can route by:
// domains only, no duplicates, bounded. The detector only ever produces
// domains; the service re-checks at its boundary, where the list arrives
// from an unprivileged process.
func ValidateHosts(hosts []string) error {
	if len(hosts) > maxHosts {
		return fmt.Errorf("%d hosts, the list holds %d", len(hosts), maxHosts)
	}
	seen := make(map[string]bool, len(hosts))
	for _, h := range hosts {
		if !isDomain(h) {
			return fmt.Errorf("%q is not a domain", h)
		}
		if len(h) > maxHostLen {
			return fmt.Errorf("%q is longer than %d", h, maxHostLen)
		}
		if strings.ContainsAny(h, " \t/\\:") {
			return fmt.Errorf("%q is not a domain", h)
		}
		if seen[h] {
			return fmt.Errorf("duplicate host %q", h)
		}
		seen[h] = true
	}
	return nil
}

// WriteRuleSet renders hosts and writes them where they stand. The core
// watches the rule set file (sing-box reloads a local source rule set on a
// write event), and a watcher does not reliably report an atomic replace
// (write temp, rename) on every platform — so the file is rewritten in place.
// A reader mid-write at worst makes the core log a reload error and keep the
// previous rules; the debounce in the watcher and the heartbeat in Run make
// that self-correcting.
func WriteRuleSet(path string, hosts []string) error {
	content, err := RuleSet(hosts)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
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
	return WriteRuleSet(path, nil)
}

func writeAtomic(path string, content []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, content, 0o644); err != nil {
		return err
	}
	return fsutil.Replace(tmp, path)
}

// Run watches the connections until ctx is done. A failed read means the core
// is down, which is an ordinary state and costs nothing. The list is published
// at start and on a heartbeat even without a change, so the rule set the
// service keeps cannot drift from this one for long.
func Run(ctx context.Context, list *List, conns func() ([]Conn, error), logf func(string, ...interface{})) {
	detector := NewDetector()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	resync := time.NewTicker(resyncInterval)
	defer resync.Stop()
	// The service's copy of the rule set can be missing or stale — its
	// directory replaced by an update, the core resumed without a push — so
	// the list is published before anything is proven, and re-published on a
	// heartbeat even without new evidence.
	if err := list.Publish(); err != nil {
		logf("[autotunnel] publish: %v", err)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
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
		case <-resync.C:
			if err := list.Publish(); err != nil {
				logf("[autotunnel] resync: %v", err)
			}
		}
	}
}
