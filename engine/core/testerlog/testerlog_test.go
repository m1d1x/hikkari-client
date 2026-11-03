package testerlog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type hub struct {
	mu     sync.Mutex
	chunks []chunk
	fail   atomic.Bool
}

func (h *hub) serve(t *testing.T) *httptest.Server {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.fail.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		var c chunk
		if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
			t.Errorf("decode: %v", err)
		}
		h.mu.Lock()
		h.chunks = append(h.chunks, c)
		h.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(s.Close)
	return s
}

func (h *hub) lines() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, c := range h.chunks {
		out = append(out, c.Lines...)
	}
	return out
}

func newShipper(url string, on *atomic.Bool, tail []string) *Shipper {
	return New(Options{
		URL:      url,
		Enabled:  on.Load,
		Backfill: func(int) []string { return tail },
		Identity: func() (string, string, string) { return "t-1", "macos", "1.0.12" },
	})
}

func TestOffSendsNothing(t *testing.T) {
	h := &hub{}
	var on atomic.Bool
	s := newShipper(h.serve(t).URL, &on, []string{"old"})
	s.follow()
	s.Tap("line")
	s.Flush(context.Background())
	if got := h.lines(); len(got) != 0 {
		t.Fatalf("sent %v while tester mode is off", got)
	}
}

func TestSwitchOnShipsBackfillThenLiveSanitized(t *testing.T) {
	h := &hub{}
	var on atomic.Bool
	s := newShipper(h.serve(t).URL, &on, []string{"before 1", "before 2"})
	on.Store(true)
	if !s.follow() {
		t.Fatal("follow must report the switch")
	}
	s.Tap("dial 203.0.113.7:443 failed")
	s.Flush(context.Background())

	got := h.lines()
	if len(got) != 4 || got[0] != "before 1" || got[1] != "before 2" || !strings.Contains(got[2], "режим тестера включён") {
		t.Fatalf("unexpected order: %q", got)
	}
	if strings.Contains(got[3], "203.0.113.7") || !strings.Contains(got[3], "[REDACTED_IP]") {
		t.Fatalf("line not sanitized: %q", got[3])
	}
	if h.chunks[0].InstallID != "t-1" || h.chunks[0].Platform != "macos" {
		t.Fatalf("identity: %+v", h.chunks[0])
	}
}

func TestBackfillDoesNotRepeatLinesTappedMeanwhile(t *testing.T) {
	if got := trimOverlap([]string{"a", "b", "c"}, []string{"b", "c", "d"}); strings.Join(got, ",") != "a" {
		t.Fatalf("got %v", got)
	}
	if got := trimOverlap([]string{"a", "b"}, []string{"x"}); strings.Join(got, ",") != "a,b" {
		t.Fatalf("got %v", got)
	}
}

func TestFailedBatchIsKeptAndOverflowCounted(t *testing.T) {
	h := &hub{}
	var on atomic.Bool
	on.Store(true)
	s := newShipper(h.serve(t).URL, &on, nil)
	s.follow()
	h.fail.Store(true)
	for i := 0; i < maxBuffered+10; i++ {
		s.Tap("x")
	}
	s.Flush(context.Background())
	h.fail.Store(false)
	s.Flush(context.Background())

	total, dropped := 0, 0
	for _, c := range h.chunks {
		total += len(c.Lines)
		dropped += c.Dropped
	}
	if total != maxBuffered || dropped != 11 { // 10 overflow + the marker line
		t.Fatalf("delivered %d lines, %d dropped", total, dropped)
	}
}

func TestSwitchOffClearsBuffer(t *testing.T) {
	h := &hub{}
	var on atomic.Bool
	on.Store(true)
	s := newShipper(h.serve(t).URL, &on, nil)
	s.follow()
	s.Tap("secret session")
	on.Store(false)
	s.follow()
	on.Store(true)
	s.Flush(context.Background())
	if got := h.lines(); len(got) != 0 {
		t.Fatalf("buffer survived switch-off: %v", got)
	}
}

// commandHub answers the first call with commands and records every chunk.
type commandHub struct {
	mu       sync.Mutex
	chunks   []chunk
	commands []command
}

func (h *commandHub) serve(t *testing.T) *httptest.Server {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var c chunk
		if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
			t.Errorf("decode: %v", err)
		}
		h.mu.Lock()
		h.chunks = append(h.chunks, c)
		commands := h.commands
		h.commands = nil
		h.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "commands": commands})
	}))
	t.Cleanup(s.Close)
	return s
}

func (h *commandHub) received() []chunk {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]chunk(nil), h.chunks...)
}

func TestHeartbeatPicksUpCommandAndResultIsAcked(t *testing.T) {
	h := &commandHub{commands: []command{
		{Kind: "run_doctor", Nonce: "n1"},
		{Kind: "rm_rf", Nonce: "n2"},
	}}
	srv := h.serve(t)
	ran := make(chan struct{}, 1)
	s := New(Options{
		URL:      srv.URL,
		Enabled:  func() bool { return true },
		Identity: func() (string, string, string) { return "t-1", "macos", "1.0.24" },
		Commands: map[string]Command{"run_doctor": {Source: "doctor", Run: func(context.Context, json.RawMessage) []string {
			ran <- struct{}{}
			return []string{"proxy={}", "direct={}"}
		}}},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.follow()
	s.Flush(ctx) // the switch-on marker
	s.heartbeat(ctx)
	go func() {
		for ctx.Err() == nil {
			select {
			case <-s.wake:
				s.Flush(ctx)
			case <-ctx.Done():
			}
		}
	}()
	<-ran

	deadline := time.Now().Add(5 * time.Second)
	var last chunk
	for time.Now().Before(deadline) {
		got := h.received()
		last = got[len(got)-1]
		if len(last.Acks) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(last.Acks) != 1 || last.Acks[0] != "n1" {
		t.Fatalf("result chunk must ack n1: %+v", last)
	}
	joined := strings.Join(last.Lines, "\n")
	for _, want := range []string{"[doctor nonce=n1] proxy={}", "[doctor nonce=n1] direct={}", "[doctor nonce=n1] ===== конец ====="} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %q", want, joined)
		}
	}
	if strings.Contains(joined, "n2") {
		t.Fatalf("an unknown kind must not run: %q", joined)
	}
	beats := 0
	for _, c := range h.received() {
		if c.Heartbeat {
			beats++
			if len(c.Lines) != 0 || c.InstallID != "t-1" {
				t.Fatalf("heartbeat: %+v", c)
			}
		}
	}
	if beats != 1 {
		t.Fatalf("heartbeats: %d", beats)
	}
}

func TestAckStaysUntilTheLastBatchAndSurvivesAFailure(t *testing.T) {
	var fail atomic.Bool
	var mu sync.Mutex
	var got []chunk
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		var c chunk
		_ = json.NewDecoder(r.Body).Decode(&c)
		mu.Lock()
		got = append(got, c)
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	s := New(Options{URL: srv.URL, Enabled: func() bool { return true },
		Identity: func() (string, string, string) { return "t-1", "macos", "1.0.24" }})
	s.mu.Lock()
	s.on = true
	// Two batches' worth of lines, the result ending in the second.
	long := strings.Repeat("x", maxLineBytes-1)
	for i := 0; i < maxBatchBytes/maxLineBytes+5; i++ {
		s.pushLocked(long)
	}
	s.acks = []string{"n1"}
	s.mu.Unlock()

	ctx := context.Background()
	fail.Store(true)
	if !s.Flush(ctx) {
		t.Fatal("a failed send still counts as sent: no heartbeat on top")
	}
	s.mu.Lock()
	kept := len(s.acks)
	s.mu.Unlock()
	if kept != 1 {
		t.Fatalf("ack lost on a failed send: %d", kept)
	}
	fail.Store(false)
	s.Flush(ctx)
	mu.Lock()
	defer mu.Unlock()
	if len(got) < 2 {
		t.Fatalf("want at least two batches, got %d", len(got))
	}
	for _, c := range got[:len(got)-1] {
		if len(c.Acks) != 0 {
			t.Fatalf("ack before the last batch: %+v", c.Acks)
		}
	}
	if a := got[len(got)-1].Acks; len(a) != 1 || a[0] != "n1" {
		t.Fatalf("last batch acks: %v", a)
	}
}
