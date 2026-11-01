// Package supportqueue delivers support reports reliably. A report is
// written to disk the moment the user sends it, with the log snapshot taken
// at that moment, and is retried in the background until the hub takes it:
// a flapping network, a tunnel that is down or an app restart must not lose
// what the user asked to send.
package supportqueue

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"NekkoVPN/core/fsutil"
)

const (
	// MaxQueued bounds the queue; past it the oldest report is dropped.
	MaxQueued = 20
	// MaxAge drops a report the hub never took: a week-old log helps no one.
	MaxAge = 7 * 24 * time.Hour

	backoffMin = 10 * time.Second
	backoffMax = 5 * time.Minute
)

// Report is one queued support report; the JSON fields are the hub's
// POST /api/v1/support/report body plus queue bookkeeping.
type Report struct {
	ID        string    `json:"report_id"`
	InstallID string    `json:"install_id"`
	Platform  string    `json:"platform"`
	Version   string    `json:"version"`
	Message   string    `json:"message"`
	Logs      string    `json:"logs"`
	Queued    time.Time `json:"queued_at"`
	Attempts  int       `json:"attempts"`
}

// Options wires the queue.
type Options struct {
	Path   string
	URL    string
	Client *http.Client
	Logf   func(format string, args ...any)
}

// Queue holds reports until the hub takes them.
type Queue struct {
	o     Options
	mu    sync.Mutex
	items []Report
	wake  chan struct{}
}

// New loads whatever an earlier run left undelivered.
func New(o Options) *Queue {
	if o.Client == nil {
		o.Client = &http.Client{Timeout: 30 * time.Second}
	}
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
	q := &Queue{o: o, wake: make(chan struct{}, 1)}
	if raw, err := os.ReadFile(o.Path); err == nil {
		if err := json.Unmarshal(raw, &q.items); err != nil {
			o.Logf("[support] queue file unreadable, starting empty: %v", err)
			q.items = nil
		}
	}
	return q
}

// Enqueue stores a report and wakes the sender. It fails only when the
// report cannot be written to disk.
func (q *Queue) Enqueue(r Report) (string, error) {
	if r.ID == "" {
		r.ID = newID()
	}
	if r.Queued.IsZero() {
		r.Queued = time.Now()
	}
	q.mu.Lock()
	q.items = append(q.items, r)
	if over := len(q.items) - MaxQueued; over > 0 {
		q.o.Logf("[support] queue full, dropping %d oldest report(s)", over)
		q.items = append([]Report(nil), q.items[over:]...)
	}
	err := q.saveLocked()
	q.mu.Unlock()
	if err != nil {
		return "", fmt.Errorf("сохранение отчёта: %w", err)
	}
	q.o.Logf("[support] report %s queued", r.ID)
	q.Wake()
	return r.ID, nil
}

// Pending returns how many reports wait for delivery.
func (q *Queue) Pending() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

// Wake asks the sender to try now.
func (q *Queue) Wake() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// Run delivers the queue until ctx is done, backing off while the hub is
// unreachable.
func (q *Queue) Run(ctx context.Context) {
	backoff := backoffMin
	var retry <-chan time.Time
	if q.Pending() > 0 {
		q.Wake()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-q.wake:
		case <-retry:
		}
		retry = nil
		err := q.sendAll(ctx)
		switch {
		case err == nil:
			backoff = backoffMin
		case ctx.Err() != nil:
			return
		default:
			q.o.Logf("[support] delivery failed, %d queued, retry in %s: %v", q.Pending(), backoff, err)
			retry = time.After(backoff)
			backoff = min(backoff*2, backoffMax)
		}
	}
}

// errRejected is a 4xx other than 429: the hub will never take the report.
var errRejected = errors.New("rejected")

func (q *Queue) sendAll(ctx context.Context) error {
	for {
		q.mu.Lock()
		q.expireLocked()
		if len(q.items) == 0 {
			q.mu.Unlock()
			return nil
		}
		r := q.items[0]
		q.items[0].Attempts++
		_ = q.saveLocked()
		q.mu.Unlock()

		err := q.send(ctx, r)
		if err != nil && !errors.Is(err, errRejected) {
			return err
		}
		if err != nil {
			q.o.Logf("[support] report %s dropped: %v", r.ID, err)
		} else {
			q.o.Logf("[support] report %s delivered (attempt %d)", r.ID, r.Attempts+1)
		}
		q.remove(r.ID)
	}
}

func (q *Queue) expireLocked() {
	kept := q.items[:0]
	for _, r := range q.items {
		if time.Since(r.Queued) > MaxAge {
			q.o.Logf("[support] report %s expired undelivered after %d attempts", r.ID, r.Attempts)
			continue
		}
		kept = append(kept, r)
	}
	q.items = kept
}

func (q *Queue) remove(id string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, r := range q.items {
		if r.ID == id {
			q.items = append(q.items[:i], q.items[i+1:]...)
			break
		}
	}
	_ = q.saveLocked()
}

func (q *Queue) send(ctx context.Context, r Report) error {
	if q.o.URL == "" {
		return errors.New("no support endpoint in this build")
	}
	body, err := json.Marshal(r)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, q.o.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := q.o.Client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	switch {
	case resp.StatusCode/100 == 2:
		return nil
	case resp.StatusCode == http.StatusTooManyRequests, resp.StatusCode/100 == 5:
		return fmt.Errorf("hub answered %d", resp.StatusCode)
	default:
		return fmt.Errorf("%w: hub answered %d", errRejected, resp.StatusCode)
	}
}

func (q *Queue) saveLocked() error {
	if q.o.Path == "" {
		return nil
	}
	raw, err := json.Marshal(q.items)
	if err != nil {
		return err
	}
	tmp := q.o.Path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return fsutil.Replace(tmp, q.o.Path)
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "sr-" + hex.EncodeToString(b[:])
}
