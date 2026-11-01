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
