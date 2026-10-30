// Package telemetry queues product events and scrubbed error reports and
// sends them to the hub in batches (docs/TELEMETRY.md). One switch,
// "help improve Hikkari", governs it; off means nothing is queued or sent.
package telemetry

import (
	"NekkoVPN/core/fsutil"
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
)

// Default is the switch's value for users who never chose. Stamped at build
// time (-X NekkoVPN/core/telemetry.Default=off); a later release can flip it
// without touching anyone's explicit choice.
var Default = "on"

// EventsURL is the hub's POST /v1/events endpoint, stamped at build time.
// Empty means the build has no endpoint: events still queue (bounded) so
// the "what is sent" screen is truthful, but nothing leaves.
var EventsURL string

const (
	SendInterval  = 15 * time.Minute
	firstDelay    = 30 * time.Second
	backoffMin    = time.Minute
	backoffMax    = time.Hour
	QueueMaxBytes = 1 << 20
	QueueMaxAge   = 7 * 24 * time.Hour
	BatchMaxItems = 200
	BatchMaxBytes = 64 << 10
	MsgMaxRunes   = 300
)

// App describes the build and platform, sent with every batch (§3).
type App struct {
	Version string `json:"version"`
	Channel string `json:"channel"`
	OS      string `json:"os"`
	OSMajor string `json:"os_major,omitempty"`
	Arch    string `json:"arch"`
}

// Event is one product event. Fields are already normalized (contract.go).
type Event struct {
	TS     int64          `json:"ts"`
	Name   string         `json:"name"`
	Fields map[string]any `json:"fields,omitempty"`
	size   int            // encoded length, for the queue bound
}

// Error is one scrubbed failure; repeats within the queue fold into Count.
type Error struct {
	TS        int64  `json:"ts"`
	Code      string `json:"code"`
	Component string `json:"component"`
	FP        string `json:"fp"`
	Msg       string `json:"msg"`
	Count     int    `json:"count"`
	size      int
}

// Batch is the envelope POSTed to the hub — the whole of what leaves.
type Batch struct {
	V       int     `json:"v"`
	Install string  `json:"install"`
	App     App     `json:"app"`
	Events  []Event `json:"events"`
	Errors  []Error `json:"errors"`
}

// Settings is the switch as the UI shows it.
type Settings struct {
	Enabled  bool `json:"enabled"`
	Explicit bool `json:"explicit"` // the user chose; Default no longer applies
	Default  bool `json:"default"`
	// Endpoint reports whether this build can send at all.
	Endpoint bool `json:"endpoint"`
}

type state struct {
	Choice       *bool  `json:"choice"`
	Install      string `json:"install,omitempty"`
	Since        int64  `json:"since,omitempty"`
	FirstConnect bool   `json:"first_connect,omitempty"`
	LastDaily    int64  `json:"last_daily,omitempty"`
}

type queue struct {
	Events []Event `json:"events"`
	Errors []Error `json:"errors"`
}

// Options wires the agent to the engine. Send is replaceable for tests.
type Options struct {
	StatePath string
	QueuePath string
	URL       string
	App       func() App
	Send      func(ctx context.Context, url string, body []byte) error
	// OnChange is told the effective switch after every change, so the
	// downdetector (governed by the same switch) follows it.
	OnChange func(enabled bool)
	Logf     func(format string, args ...any)
	Now      func() time.Time
}

type Agent struct {
	o Options

	mu    sync.Mutex
	st    state
	q     queue
	last  *Batch
	fresh bool // no state file existed: this is a clean install
	wake  chan struct{}
	// saving is set while a deferred queue write is pending: enqueueing is
	// frequent, the file is up to QueueMaxBytes, so writes are coalesced.
	saving bool
}

// saveDelay coalesces queue writes. A crash loses at most this much.
const saveDelay = 5 * time.Second

func New(o Options) *Agent {
	if o.Send == nil {
		o.Send = post
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
	if o.App == nil {
		o.App = func() App { return App{} }
	}
	return &Agent{o: o, wake: make(chan struct{}, 1)}
}

// Load reads the switch and the queue. A missing state file marks a clean
// install; a malformed one keeps the defaults.
func (a *Agent) Load() {
	a.mu.Lock()
	defer a.mu.Unlock()
	raw, err := os.ReadFile(a.o.StatePath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		a.fresh = true
	case err == nil:
		_ = json.Unmarshal(raw, &a.st)
	}
	if raw, err := os.ReadFile(a.o.QueuePath); err == nil {
		_ = json.Unmarshal(raw, &a.q)
	}
	for i := range a.q.Events {
		a.q.Events[i].size = jsonSize(a.q.Events[i])
	}
	for i := range a.q.Errors {
		a.q.Errors[i].size = jsonSize(a.q.Errors[i])
	}
	if !a.enabledLocked() {
		a.q = queue{}
	}
}

// Start records the launch (and the install on a clean one). Call once
// after Load.
func (a *Agent) Start(cold bool) {
	a.mu.Lock()
	fresh := a.fresh
	a.fresh = false
	a.mu.Unlock()
	if fresh {
		a.Event("install", nil)
		a.Flush()
	}
	a.Event("launch", map[string]any{"cold": cold})
	a.mu.Lock()
	_ = a.saveStateLocked()
	a.mu.Unlock()
}

func defaultOn() bool { return Default != "off" }

func (a *Agent) enabledLocked() bool {
	if a.st.Choice != nil {
		return *a.st.Choice
	}
	return defaultOn()
}

func (a *Agent) Settings() Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	return Settings{
		Enabled:  a.enabledLocked(),
		Explicit: a.st.Choice != nil,
		Default:  defaultOn(),
		Endpoint: a.o.URL != "",
	}
}

// Enabled is the effective switch.
func (a *Agent) Enabled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.enabledLocked()
}

// SetEnabled records the user's explicit choice. Off wipes the queue and
// the install ID; turning it back on starts a new, unrelated ID.
func (a *Agent) SetEnabled(on bool) error {
	a.mu.Lock()
	a.st.Choice = &on
	if !on {
		a.q = queue{}
		a.last = nil
		a.st.Install, a.st.Since, a.st.FirstConnect, a.st.LastDaily = "", 0, false, 0
	}
	a.saving = false
	err := errors.Join(a.saveQueueLocked(), a.saveStateLocked())
	a.mu.Unlock()
	if a.o.OnChange != nil {
		a.o.OnChange(on)
	}
	return err
}

// Event queues a product event after checking it against the contract.
// Off → silently nothing; a contract violation → error.
func (a *Agent) Event(name string, fields map[string]any) error {
	norm, err := normalize(name, fields)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.enabledLocked() {
		return nil
	}
	a.ensureInstallLocked()
	e := Event{TS: a.minuteLocked(), Name: name, Fields: norm}
	e.size = jsonSize(e)
	a.q.Events = append(a.q.Events, e)
	a.trimLocked()
	a.saveQueueSoonLocked()
	return nil
}

// FirstConnect queues first_connect_ok once per install ID; later calls do
// nothing. Call on every successful connection.
func (a *Agent) FirstConnect(engine, proto string) error {
	a.mu.Lock()
	if !a.enabledLocked() || a.st.FirstConnect {
		a.mu.Unlock()
		return nil
	}
	a.ensureInstallLocked()
	since := int(a.o.Now().Unix() - a.st.Since)
	a.mu.Unlock()
	if err := a.Event("first_connect_ok", map[string]any{"engine": engine, "proto": proto, "secs_since_install": since}); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.st.FirstConnect = true
	return a.saveStateLocked()
}

// DailyDue reports whether a daily_active event is owed for today and, if
// so, marks it sent. The caller then queues it with its own counters.
func (a *Agent) DailyDue() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.enabledLocked() {
		return false
	}
	day := a.o.Now().UTC().Truncate(24 * time.Hour).Unix()
	if a.st.LastDaily >= day {
		return false
	}
	a.st.LastDaily = day
	_ = a.saveStateLocked()
	return true
}

// Error queues a failure: the message is scrubbed and cut, repeats of the
// same fingerprint fold into one entry.
func (a *Agent) Error(code, component, msg string) error {
	if !errorCodes[code] {
		return fmt.Errorf("telemetry: unknown error code %q", code)
	}
	if !components[component] {
		return fmt.Errorf("telemetry: unknown component %q", component)
	}
	clean := truncateRunes(Scrub(msg), MsgMaxRunes)
	fp := Fingerprint(code, clean)
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.enabledLocked() {
		return nil
	}
	a.ensureInstallLocked()
	for i := range a.q.Errors {
		if a.q.Errors[i].FP == fp {
			a.q.Errors[i].Count++
			a.q.Errors[i].TS = a.minuteLocked()
			a.q.Errors[i].size = jsonSize(a.q.Errors[i])
			a.saveQueueSoonLocked()
			return nil
		}
	}
	e := Error{TS: a.minuteLocked(), Code: code, Component: component, FP: fp, Msg: clean, Count: 1}
	e.size = jsonSize(e)
	a.q.Errors = append(a.q.Errors, e)
	a.trimLocked()
	a.saveQueueSoonLocked()
	return nil
}

// LastBatch is the most recent batch exactly as sent (or as it would be,
// without an endpoint), for the "what is sent" screen.
func (a *Agent) LastBatch() *Batch {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.last == nil {
		if b, _, _ := a.nextBatchLocked(); b != nil {
			return b
		}
		return nil
	}
	cp := *a.last
	return &cp
}

// Flush asks Run to send now instead of at the next interval.
func (a *Agent) Flush() {
	select {
	case a.wake <- struct{}{}:
	default:
	}
}

// Run sends queued items every SendInterval, with exponential backoff on
// failure. Blocks until ctx is done.
func (a *Agent) Run(ctx context.Context) {
	delay := firstDelay
	backoff := backoffMin
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		case <-a.wake:
		}
		err := a.sendAll(ctx)
		switch {
		case err == nil:
			delay, backoff = SendInterval, backoffMin
		case ctx.Err() != nil:
			return
		default:
			a.o.Logf("[telemetry] send failed, retry in %s: %v", backoff, err)
			delay = backoff
			backoff = min(backoff*2, backoffMax)
		}
	}
}

// errRejected is a 4xx from the hub: the batch will never be accepted, so
// it is dropped instead of retried forever.
var errRejected = errors.New("rejected")

func (a *Agent) sendAll(ctx context.Context) error {
	for {
		a.mu.Lock()
		if !a.enabledLocked() || a.o.URL == "" {
			a.mu.Unlock()
			return nil
		}
		batch, body, n := a.nextBatchLocked()
		a.mu.Unlock()
		if batch == nil {
			return nil
		}
		err := a.o.Send(ctx, a.o.URL, body)
		if err != nil && !errors.Is(err, errRejected) {
			return err
		}
		if err != nil {
			a.o.Logf("[telemetry] hub rejected a batch of %d events, %d errors; dropped", len(batch.Events), len(batch.Errors))
		}
		a.mu.Lock()
		// The switch may have flipped while sending; off already emptied it.
		if a.enabledLocked() {
			a.dropSentLocked(n)
			a.last = batch
			a.saveQueueSoonLocked()
		}
		a.mu.Unlock()
	}
}

type taken struct{ events, errors int }

// nextBatchLocked takes the oldest items that fit the batch limits.
func (a *Agent) nextBatchLocked() (*Batch, []byte, taken) {
	if len(a.q.Events) == 0 && len(a.q.Errors) == 0 {
		return nil, nil, taken{}
	}
	b := &Batch{V: 1, Install: a.st.Install, App: a.o.App(), Events: []Event{}, Errors: []Error{}}
	var n taken
	for {
		grew := false
		if n.errors < len(a.q.Errors) && n.events+n.errors < BatchMaxItems {
			b.Errors = append(b.Errors, a.q.Errors[n.errors])
			if size(b) > BatchMaxBytes && n.events+n.errors > 0 {
				b.Errors = b.Errors[:len(b.Errors)-1]
			} else {
				n.errors++
				grew = true
			}
		}
		if n.events < len(a.q.Events) && n.events+n.errors < BatchMaxItems {
			b.Events = append(b.Events, a.q.Events[n.events])
			if size(b) > BatchMaxBytes && n.events+n.errors > 0 {
				b.Events = b.Events[:len(b.Events)-1]
			} else {
				n.events++
				grew = true
			}
		}
		if !grew {
			break
		}
	}
	body, _ := json.Marshal(b)
	return b, body, n
}

func (a *Agent) dropSentLocked(n taken) {
	a.q.Events = append([]Event(nil), a.q.Events[min(n.events, len(a.q.Events)):]...)
	a.q.Errors = append([]Error(nil), a.q.Errors[min(n.errors, len(a.q.Errors)):]...)
}

// trimLocked enforces §9: nothing older than QueueMaxAge, and under
// QueueMaxBytes by dropping the oldest events first, then the oldest errors.
func (a *Agent) trimLocked() {
	cutoff := a.o.Now().Add(-QueueMaxAge).Unix()
	ev := a.q.Events[:0]
	for _, e := range a.q.Events {
		if e.TS >= cutoff {
			ev = append(ev, e)
		}
	}
	a.q.Events = ev
	er := a.q.Errors[:0]
	for _, e := range a.q.Errors {
		if e.TS >= cutoff {
			er = append(er, e)
		}
	}
	a.q.Errors = er
	// Each item carries its encoded length, so the bound holds without
	// re-encoding the whole queue on every enqueue.
	total := len(`{"events":[],"errors":[]}`)
	for _, e := range a.q.Events {
		total += e.size + 1
	}
	for _, e := range a.q.Errors {
		total += e.size + 1
	}
	dropEv, dropEr := 0, 0
	for total > QueueMaxBytes && dropEv+dropEr < len(a.q.Events)+len(a.q.Errors) {
		if dropEv < len(a.q.Events) {
			total -= a.q.Events[dropEv].size + 1
			dropEv++
		} else {
			total -= a.q.Errors[dropEr].size + 1
			dropEr++
		}
	}
	a.q.Events = a.q.Events[dropEv:]
	a.q.Errors = a.q.Errors[dropEr:]
}

func jsonSize(v any) int {
	raw, _ := json.Marshal(v)
	return len(raw)
}

func (a *Agent) ensureInstallLocked() {
	if a.st.Install != "" {
		return
	}
	var b [16]byte
	_, _ = rand.Read(b[:])
	a.st.Install = "t-" + hex.EncodeToString(b[:])
	a.st.Since = a.o.Now().Unix()
	_ = a.saveStateLocked()
}

func (a *Agent) minuteLocked() int64 { return a.o.Now().Truncate(time.Minute).Unix() }

func (a *Agent) saveStateLocked() error { return writeJSON(a.o.StatePath, a.st) }
func (a *Agent) saveQueueLocked() error { return writeJSON(a.o.QueuePath, a.q) }

func (a *Agent) saveQueueSoonLocked() {
	if a.saving {
		return
	}
	a.saving = true
	time.AfterFunc(saveDelay, func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.saving {
			a.saving = false
			_ = a.saveQueueLocked()
		}
	})
}

// Close writes a pending queue change now. Call on shutdown.
func (a *Agent) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.saving {
		return nil
	}
	a.saving = false
	return a.saveQueueLocked()
}

func writeJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0600); err != nil {
		return err
	}
	return fsutil.Replace(tmp, path)
}

func size(b *Batch) int {
	raw, _ := json.Marshal(b)
	return len(raw)
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

var client = &http.Client{Timeout: 20 * time.Second}

func post(ctx context.Context, url string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests:
		return fmt.Errorf("%w: HTTP %d", errRejected, resp.StatusCode)
	}
	return fmt.Errorf("HTTP %d", resp.StatusCode)
}
