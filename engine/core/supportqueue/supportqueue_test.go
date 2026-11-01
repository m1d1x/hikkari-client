package supportqueue

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type hub struct {
	status atomic.Int32
	mu     sync.Mutex
	got    []Report
}

func (h *hub) server(t *testing.T) *httptest.Server {
	h.status.Store(http.StatusCreated)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		code := int(h.status.Load())
		if code/100 == 2 {
			var rep Report
			_ = json.NewDecoder(r.Body).Decode(&rep)
			h.mu.Lock()
			h.got = append(h.got, rep)
			h.mu.Unlock()
		}
		w.WriteHeader(code)
	}))
	t.Cleanup(s.Close)
	return s
}

func (h *hub) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.got)
}

func TestReportSurvivesOutageAndRestart(t *testing.T) {
	h := &hub{}
	srv := h.server(t)
	path := filepath.Join(t.TempDir(), "support-outbox.json")
	h.status.Store(http.StatusServiceUnavailable)

	q := New(Options{Path: path, URL: srv.URL})
	id, err := q.Enqueue(Report{InstallID: "t-1", Message: "лагает", Logs: "log"})
	if err != nil || id == "" {
		t.Fatalf("enqueue: %q %v", id, err)
	}
	if err := q.sendAll(context.Background()); err == nil {
		t.Fatal("503 must be retried, not reported as delivered")
	}

	// The app restarts with the hub still down: the report is on disk.
	q = New(Options{Path: path, URL: srv.URL})
	if q.Pending() != 1 || q.items[0].Attempts != 1 {
		t.Fatalf("after restart: %+v", q.items)
	}

	h.status.Store(http.StatusCreated)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Run(ctx)
	deadline := time.Now().Add(2 * time.Second)
	for h.count() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("queued report never delivered")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if h.got[0].ID != id || h.got[0].Message != "лагает" {
		t.Fatalf("delivered %+v", h.got[0])
	}
	for q.Pending() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("delivered report still queued")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if New(Options{Path: path}).Pending() != 0 {
		t.Fatal("delivered report still on disk")
	}
}

func TestRejectedReportIsDroppedNotRetried(t *testing.T) {
	h := &hub{}
	srv := h.server(t)
	h.status.Store(http.StatusBadRequest)
	q := New(Options{Path: filepath.Join(t.TempDir(), "q.json"), URL: srv.URL})
	_, _ = q.Enqueue(Report{Message: "x"})
	if err := q.sendAll(context.Background()); err != nil || q.Pending() != 0 {
		t.Fatalf("400 must drop the report: err=%v pending=%d", err, q.Pending())
	}
}

func TestRateLimitIsRetried(t *testing.T) {
	h := &hub{}
	srv := h.server(t)
	h.status.Store(http.StatusTooManyRequests)
	q := New(Options{Path: filepath.Join(t.TempDir(), "q.json"), URL: srv.URL})
	_, _ = q.Enqueue(Report{Message: "x"})
	if err := q.sendAll(context.Background()); err == nil || q.Pending() != 1 {
		t.Fatalf("429 must keep the report: err=%v pending=%d", err, q.Pending())
	}
}

func TestQueueIsBoundedAndExpires(t *testing.T) {
	q := New(Options{Path: filepath.Join(t.TempDir(), "q.json")})
	for i := 0; i < MaxQueued+3; i++ {
		_, _ = q.Enqueue(Report{Message: "x"})
	}
	if q.Pending() != MaxQueued {
		t.Fatalf("pending %d, want %d", q.Pending(), MaxQueued)
	}
	q.items[0].Queued = time.Now().Add(-MaxAge - time.Hour)
	q.mu.Lock()
	q.expireLocked()
	q.mu.Unlock()
	if q.Pending() != MaxQueued-1 {
		t.Fatalf("expired report kept: %d", q.Pending())
	}
}
