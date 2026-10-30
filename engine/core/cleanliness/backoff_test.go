package cleanliness

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

type testLogger struct{ lines []string }

func (l *testLogger) Logf(format string, args ...interface{}) {
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func TestBackoffReadyUntilFirstFailure(t *testing.T) {
	b := newProbeBackoff("x")
	if ok, _ := b.ready(); !ok {
		t.Fatal("a fresh backoff must allow the first attempt")
	}
	b.fail("boom", 0)
	ok, left := b.ready()
	if ok {
		t.Fatal("must not be ready right after a failure")
	}
	if left <= 0 {
		t.Fatalf("remaining wait = %s, want positive", left)
	}
}

func TestBackoffGrowsAndCaps(t *testing.T) {
	b := newProbeBackoff("x")
	var prev time.Duration
	for i := 0; i < 12; i++ {
		d := b.fail("boom", 0)
		if d > backoffMax {
			t.Fatalf("attempt %d: delay %s exceeds the %s cap", i, d, backoffMax)
		}
		// Jitter is ±20%, so growth is checked with that slack.
		if i > 0 && i < 6 && d < prev {
			t.Errorf("attempt %d: delay %s did not grow past %s", i, d, prev)
		}
		prev = d
	}
	if d := b.fail("boom", 0); d != backoffMax {
		t.Errorf("saturated delay = %s, want exactly %s", d, backoffMax)
	}
}

func TestBackoffSucceedClearsImmediately(t *testing.T) {
	b := newProbeBackoff("x")
	b.fail("boom", 0)
	b.succeed()
	if ok, _ := b.ready(); !ok {
		t.Fatal("succeed must clear the backoff")
	}
	if n, reason := b.state(); n != 0 || reason != "" {
		t.Fatalf("state after succeed = (%d, %q), want (0, \"\")", n, reason)
	}
	// And the next failure starts from the base delay again, not where the
	// previous run left off.
	if d := b.fail("boom", 0); d > backoffBase*2 {
		t.Errorf("delay after a success = %s, want ~%s", d, backoffBase)
	}
}

// A server that says when to come back wins over the computed delay — that is
// the whole reason ip-api's X-Ttl is read.
func TestBackoffHonoursServerRetryAfter(t *testing.T) {
	b := newProbeBackoff("x")
	if d := b.fail("limited", 47*time.Second); d != 47*time.Second {
		t.Fatalf("delay = %s, want 47s", d)
	}
}

// …but never below one refresh round, or a "retry after 1s" header would
// reinstate exactly the hammering this exists to stop.
func TestBackoffFloorsShortRetryAfter(t *testing.T) {
	b := newProbeBackoff("x")
	if d := b.fail("limited", time.Second); d != backoffBase {
		t.Fatalf("delay = %s, want the %s floor", d, backoffBase)
	}
}

func TestRetryAfterFromResponse(t *testing.T) {
	mk := func(h, v string) *http.Response {
		r := &http.Response{Header: http.Header{}}
		r.Header.Set(h, v)
		return r
	}
	if got := retryAfterFromResponse(mk("X-Ttl", "47")); got != 47*time.Second {
		t.Errorf("X-Ttl: got %s, want 47s", got)
	}
	if got := retryAfterFromResponse(mk("Retry-After", "30")); got != 30*time.Second {
		t.Errorf("Retry-After: got %s, want 30s", got)
	}
	if got := retryAfterFromResponse(mk("Retry-After", "nonsense")); got != 0 {
		t.Errorf("unparseable: got %s, want 0", got)
	}
	if got := retryAfterFromResponse(&http.Response{Header: http.Header{}}); got != 0 {
		t.Errorf("absent: got %s, want 0", got)
	}
	if got := retryAfterFromResponse(nil); got != 0 {
		t.Errorf("nil response: got %s, want 0", got)
	}
	// An HTTP-date Retry-After in the past yields no delay, not a negative one.
	past := mk("Retry-After", time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat))
	if got := retryAfterFromResponse(past); got != 0 {
		t.Errorf("past date: got %s, want 0", got)
	}
}

func TestRateLimitedClassifiesResponses(t *testing.T) {
	mk := func(code int) *http.Response {
		return &http.Response{StatusCode: code, Header: http.Header{}}
	}
	if err := rateLimited("s", mk(200)); err != nil {
		t.Errorf("200: got %v, want nil", err)
	}
	for _, code := range []int{429, 503} {
		err := rateLimited("s", mk(code))
		if _, ok := err.(*rateLimitError); !ok {
			t.Errorf("%d: got %T (%v), want *rateLimitError", code, err, err)
		}
	}
	err := rateLimited("s", mk(500))
	if err == nil {
		t.Error("500: want an error")
	}
	if _, ok := err.(*rateLimitError); ok {
		t.Error("500: must not be classified as a rate limit")
	}
}

// ---- end to end through the Checker --------------------------------------

// The regression: ip-api failing meant a failed request every 20s forever.
// One failure must now buy at least one skipped round, and the skipped round
// must not touch the network.
func TestProbeOnceSkipsIPAPIWhileBackingOff(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)

	log := &testLogger{}
	c := New(log, func() bool { return true }, "")
	c.ipAPIBackoff.fail("primed", 0) // stand in for a first failed round

	rep := c.probeOnce()
	if atomic.LoadInt32(&hits) != 0 {
		t.Error("a backing-off source was queried")
	}
	if rep.Verdict != "checking" {
		t.Errorf("verdict = %q, want %q with nothing cached yet", rep.Verdict, "checking")
	}
	if rep.Message == "" {
		t.Error("the held report must explain the wait")
	}
}

// A backoff must not blank a card that already had data: the address has not
// changed, only our ability to re-ask about it.
func TestProbeOnceServesLastGoodReportDuringBackoff(t *testing.T) {
	log := &testLogger{}
	c := New(log, func() bool { return true }, "")
	c.mu.Lock()
	c.cached = Report{
		Running: true, Checked: true, IP: "31.77.17.245",
		Country: "NL", ISP: "AEZA", Verdict: "dirty", Score: 40,
		CheckedAt: "2026-09-26T16:00:00Z",
	}
	c.mu.Unlock()
	c.ipAPIBackoff.fail("limited", 0)

	rep := c.probeOnce()
	if rep.IP != "31.77.17.245" || rep.Verdict != "dirty" || rep.Score != 40 {
		t.Fatalf("held report lost data: %+v", rep)
	}
	if !rep.Running {
		t.Error("held report must still read as running")
	}
	if rep.Message == "" {
		t.Error("held report must say the data is stale")
	}
}

func TestProbeOnceReportsOffWithoutTunnel(t *testing.T) {
	c := New(&testLogger{}, func() bool { return false }, "")
	if rep := c.probeOnce(); rep.Verdict != "off" || rep.Running {
		t.Fatalf("got %+v, want an off report", rep)
	}
}

// Skipping ipapi.is must not read as "clean": the flags it produced last time
// carry over for as long as the address is the same.
func TestCarryOverIntelKeepsFlagsForTheSameIP(t *testing.T) {
	c := New(&testLogger{}, func() bool { return true }, "")
	c.mu.Lock()
	c.cached = Report{
		Checked: true, IP: "31.77.17.245",
		IsHosting: true, IsVPN: true, ASNType: "hosting",
		ASNCountry: "NL", AbuserLabel: "High",
	}
	c.mu.Unlock()

	rep := Report{IP: "31.77.17.245"}
	c.carryOverIntel(&rep)
	if !rep.IsHosting || !rep.IsVPN || rep.ASNType != "hosting" ||
		rep.ASNCountry != "NL" || rep.AbuserLabel != "High" {
		t.Fatalf("flags not carried over: %+v", rep)
	}
}

func TestCarryOverIntelIgnoresADifferentIP(t *testing.T) {
	c := New(&testLogger{}, func() bool { return true }, "")
	c.mu.Lock()
	c.cached = Report{Checked: true, IP: "31.77.17.245", IsHosting: true, ASNType: "hosting"}
	c.mu.Unlock()

	rep := Report{IP: "203.0.113.9"}
	c.carryOverIntel(&rep)
	if rep.IsHosting || rep.ASNType != "" {
		t.Fatalf("stale flags applied to a new address: %+v", rep)
	}
}

func TestNoteProbeFailureLogsTheNextTry(t *testing.T) {
	log := &testLogger{}
	c := New(log, func() bool { return true }, "")
	c.noteProbeFailure(c.ipAPIBackoff, fmt.Errorf("EOF"))
	if len(log.lines) != 1 {
		t.Fatalf("logged %d lines, want 1", len(log.lines))
	}
	line := log.lines[0]
	for _, want := range []string{"ip-api", "next try in", "EOF", "1 in a row"} {
		if !containsStr(line, want) {
			t.Errorf("line %q is missing %q", line, want)
		}
	}
}

func containsStr(h, n string) bool {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return true
		}
	}
	return false
}
