package downdetector

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

func TestAgentDrainAndAckOutbox(t *testing.T) {
	a := newAgent(t, []Server{{Profile: "pub", ID: "n:fra-1"}})

	// Empty outbox
	if got := a.DrainOutbox(5); got != nil {
		t.Fatalf("DrainOutbox on empty agent returned %+v, want nil", got)
	}
	if got := a.DrainOutbox(-1); got != nil {
		t.Fatalf("DrainOutbox(-1) returned %+v, want nil", got)
	}
	a.AckOutbox(1) // should not panic

	// Add 3 reports
	a.tick(t0, t0)
	a.tick(t0, t0.Add(Window))
	a.tick(t0, t0.Add(2*Window))
	if n := len(a.Outbox()); n != 3 {
		t.Fatalf("outbox len %d, want 3", n)
	}

	// Drain 2 reports - outbox must remain intact
	drained := a.DrainOutbox(2)
	if len(drained) != 2 {
		t.Fatalf("DrainOutbox(2) returned %d reports, want 2", len(drained))
	}
	if n := len(a.Outbox()); n != 3 {
		t.Fatalf("outbox len after drain %d, want 3", n)
	}

	// Mutating drained slice shouldn't mutate agent outbox
	drained[0].Install = "corrupted"
	if a.Outbox()[0].Install == "corrupted" {
		t.Fatal("DrainOutbox did not return an isolated copy")
	}

	// Ack 1 report
	a.AckOutbox(1)
	if n := len(a.Outbox()); n != 2 {
		t.Fatalf("outbox len after AckOutbox(1) %d, want 2", n)
	}

	// Verify persistence survives reload
	reloaded := New(a.statePath, a.outboxPath, a.deps)
	reloaded.Load()
	if n := len(reloaded.Outbox()); n != 2 {
		t.Fatalf("persisted outbox len %d, want 2", n)
	}

	// Ack remaining (count > len)
	a.AckOutbox(10)
	if n := len(a.Outbox()); n != 0 {
		t.Fatalf("outbox len after AckOutbox(10) %d, want 0", n)
	}

	reloaded2 := New(a.statePath, a.outboxPath, a.deps)
	reloaded2.Load()
	if n := len(reloaded2.Outbox()); n != 0 {
		t.Fatalf("persisted outbox len %d, want 0", n)
	}
}

func TestSenderSuccess(t *testing.T) {
	var mu sync.Mutex
	var received []Report
	var authHeaders []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("got method %s, want POST", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path != "/v1/reports" {
			t.Errorf("got path %s, want /v1/reports", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("got Content-Type %s, want application/json", r.Header.Get("Content-Type"))
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body error: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		var rep Report
		if err := json.Unmarshal(body, &rep); err != nil {
			t.Errorf("unmarshal report error: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		mu.Lock()
		authHeaders = append(authHeaders, r.Header.Get("Authorization"))
		received = append(received, rep)
		mu.Unlock()

		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	a := newAgent(t, []Server{{Profile: "pub", ID: "n:fra-1"}})
	a.tick(t0, t0)
	a.tick(t0, t0.Add(Window))
	if n := len(a.Outbox()); n != 2 {
		t.Fatalf("expected 2 reports in outbox, got %d", n)
	}

	token := "secret-invite-token"
	sender := NewSender(a, srv.URL+"/v1/reports", token, srv.Client())
	sender.Interval = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go sender.Run(ctx)

	waitFor(t, 2*time.Second, func() bool {
		return len(a.Outbox()) == 0
	})

	mu.Lock()
	recCount := len(received)
	auths := append([]string(nil), authHeaders...)
	mu.Unlock()

	if recCount != 2 {
		t.Fatalf("server received %d reports, want 2", recCount)
	}
	for i, hdr := range auths {
		if want := "Bearer " + token; hdr != want {
			t.Errorf("report %d: auth header %q, want %q", i, hdr, want)
		}
	}

	// Verify disk persistence after sending
	b := New(a.statePath, a.outboxPath, a.deps)
	b.Load()
	if n := len(b.Outbox()); n != 0 {
		t.Fatalf("persisted outbox not empty after ack: %d", n)
	}
}

func TestSenderDropsRejectedReportAndSendsTheRest(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	a := newAgent(t, []Server{{Profile: "pub", ID: "n:fra-1"}})
	a.tick(t0, t0)
	a.tick(t0, t0.Add(Window))

	sender := NewSender(a, srv.URL, "", srv.Client())
	sender.Interval = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sender.Run(ctx)

	waitFor(t, 2*time.Second, func() bool { return len(a.Outbox()) == 0 })
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("server saw %d attempts, want 2", got)
	}
}

func TestSenderFailure500AndRetry(t *testing.T) {
	var attempts int32
	firstFailedCh := make(chan struct{}, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		att := atomic.AddInt32(&attempts, 1)
		if att == 1 {
			select {
			case firstFailedCh <- struct{}{}:
			default:
			}
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	a := newAgent(t, []Server{{Profile: "pub", ID: "n:fra-1"}})
	a.tick(t0, t0)
	if n := len(a.Outbox()); n != 1 {
		t.Fatalf("expected 1 report in outbox, got %d", n)
	}

	sender := NewSender(a, srv.URL+"/v1/reports", "fail-token", srv.Client())
	sender.Interval = 15 * time.Millisecond
	sender.minBackoff = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go sender.Run(ctx)

	// Wait for the first attempt to fail
	select {
	case <-firstFailedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first attempt")
	}

	// Verify report is still present in outbox right after 500 error
	if n := len(a.Outbox()); n != 1 {
		t.Fatalf("report must stay in outbox after 500 error, got %d", n)
	}

	// Verify report is eventually retried and cleared
	waitFor(t, 2*time.Second, func() bool {
		return len(a.Outbox()) == 0
	})

	if att := atomic.LoadInt32(&attempts); att < 2 {
		t.Fatalf("expected at least 2 attempts, got %d", att)
	}
}

func TestSenderStatus429AndRetry(t *testing.T) {
	var attempts int32
	firstFailedCh := make(chan struct{}, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		att := atomic.AddInt32(&attempts, 1)
		if att == 1 {
			select {
			case firstFailedCh <- struct{}{}:
			default:
			}
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	a := newAgent(t, []Server{{Profile: "pub", ID: "n:fra-1"}})
	a.tick(t0, t0)
	if n := len(a.Outbox()); n != 1 {
		t.Fatalf("expected 1 report in outbox, got %d", n)
	}

	sender := NewSender(a, srv.URL+"/v1/reports", "429-token", srv.Client())
	sender.Interval = 15 * time.Millisecond
	sender.minBackoff = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go sender.Run(ctx)

	select {
	case <-firstFailedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for 429 attempt")
	}

	if n := len(a.Outbox()); n != 1 {
		t.Fatalf("report must stay in outbox after 429, got %d", n)
	}

	waitFor(t, 2*time.Second, func() bool {
		return len(a.Outbox()) == 0
	})

	if att := atomic.LoadInt32(&attempts); att < 2 {
		t.Fatalf("expected at least 2 attempts, got %d", att)
	}
}

func TestSenderNetworkError(t *testing.T) {
	// Start and immediately close a server so connections fail
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srvURL := srv.URL
	srv.Close()

	a := newAgent(t, []Server{{Profile: "pub", ID: "n:fra-1"}})
	a.tick(t0, t0)
	if n := len(a.Outbox()); n != 1 {
		t.Fatalf("expected 1 report in outbox, got %d", n)
	}

	sender := NewSender(a, srvURL+"/v1/reports", "net-err-token", http.DefaultClient)
	sender.Interval = 10 * time.Millisecond
	sender.minBackoff = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go sender.Run(ctx)

	// Sleep slightly to let at least one tick trigger a failed network request
	time.Sleep(50 * time.Millisecond)

	// Report must remain in outbox
	if n := len(a.Outbox()); n != 1 {
		t.Fatalf("report must remain in outbox on network error, got %d", n)
	}
}

func TestSenderContextDone(t *testing.T) {
	a := newAgent(t, []Server{{Profile: "pub", ID: "n:fra-1"}})
	sender := NewSender(a, "http://127.0.0.1:0/v1/reports", "token", nil)
	sender.Interval = 50 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		sender.Run(ctx)
		close(done)
	}()

	// Cancel context and verify Run exits
	cancel()

	select {
	case <-done:
		// OK
	case <-time.After(time.Second):
		t.Fatal("sender.Run did not terminate on ctx.Done()")
	}
}
