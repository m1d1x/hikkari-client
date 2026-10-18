package health

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// withEchoServices swaps the probe's service list for the duration of a test.
func withEchoServices(t *testing.T, urls []string) {
	t.Helper()
	saved := echoServices
	echoServices = urls
	t.Cleanup(func() { echoServices = saved })
}

func echoServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, body)
	}))
	t.Cleanup(s.Close)
	return s
}

// The regression: one slow service is not a tunnel failure. Reading it as one
// is what made the UI flap ok -> connecting -> ok every half-minute.
func TestFetchExternalIPFallsBackPastAFailingService(t *testing.T) {
	var hits int32
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(dead.Close)
	good := echoServer(t, "203.0.113.7\n")

	withEchoServices(t, []string{dead.URL, good.URL})

	ip, err := fetchExternalIP()
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if ip != "203.0.113.7" {
		t.Fatalf("got %q, want 203.0.113.7", ip)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Errorf("failing service hit %d times, want 1", hits)
	}
}

func TestFetchExternalIPStopsAtTheFirstAnswer(t *testing.T) {
	first := echoServer(t, "198.51.100.2")
	var secondHits int32
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&secondHits, 1)
		fmt.Fprint(w, "203.0.113.9")
	}))
	t.Cleanup(second.Close)

	withEchoServices(t, []string{first.URL, second.URL})

	ip, err := fetchExternalIP()
	if err != nil || ip != "198.51.100.2" {
		t.Fatalf("got %q, %v", ip, err)
	}
	if secondHits != 0 {
		t.Errorf("second service was queried %d times, want 0", secondHits)
	}
}

func TestFetchExternalIPErrorNamesHowManyWereTried(t *testing.T) {
	bad := echoServer(t, "not-an-ip")
	worse := echoServer(t, "<html>captive portal</html>")

	withEchoServices(t, []string{bad.URL, worse.URL})

	_, err := fetchExternalIP()
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "2") {
		t.Errorf("error %q does not report the number of services tried", err)
	}
}

// A captive portal answering with a large HTML body must not be read whole.
func TestFetchIPFromRejectsNonIPBodyWithoutSlurpingIt(t *testing.T) {
	huge := strings.Repeat("x", 1<<20)
	s := echoServer(t, huge)
	if _, err := fetchIPFrom(s.URL, 2*time.Second); err == nil {
		t.Fatal("expected an error for a non-ip body")
	}
}

func TestFetchIPFromTrimsWhitespace(t *testing.T) {
	s := echoServer(t, "  192.0.2.55 \n")
	ip, err := fetchIPFrom(s.URL, 2*time.Second)
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if ip != "192.0.2.55" {
		t.Fatalf("got %q", ip)
	}
}

func TestFetchIPFromRejectsNon200(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, "192.0.2.1")
	}))
	t.Cleanup(s.Close)
	if _, err := fetchIPFrom(s.URL, 2*time.Second); err == nil {
		t.Fatal("expected an error for a non-200 answer")
	}
}

// ---- baseline refusal log ----

type countingLogger struct {
	lines []string
}

func (l *countingLogger) Logf(format string, args ...interface{}) {
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *countingLogger) count(substr string) int {
	n := 0
	for _, s := range l.lines {
		if strings.Contains(s, substr) {
			n++
		}
	}
	return n
}

// The regression: the 2s probe loop logged this refusal on every pass, which
// buried the real startup failure under hundreds of identical lines.
func TestBaselineRefusalIsLoggedOncePerAddress(t *testing.T) {
	log := &countingLogger{}
	c := New(t.TempDir()+"/direct-ip", log, func() []string { return nil })
	c.mu.Lock()
	c.tunnelIP = "31.77.17.245"
	c.mu.Unlock()

	for i := 0; i < 50; i++ {
		c.maybeBaseline("31.77.17.245")
	}
	if got := log.count("refusing to set baseline"); got != 1 {
		t.Fatalf("logged %d refusals for one address, want 1", got)
	}

	// A different address is news again.
	c.mu.Lock()
	c.tunnelIP = "203.0.113.1"
	c.mu.Unlock()
	c.maybeBaseline("203.0.113.1")
	if got := log.count("refusing to set baseline"); got != 2 {
		t.Fatalf("logged %d refusals for two addresses, want 2", got)
	}
}

// Suppression must not stop a legitimate baseline from being adopted, nor
// from being logged when it changes.
func TestBaselineStillAdoptedAfterRefusals(t *testing.T) {
	log := &countingLogger{}
	c := New(t.TempDir()+"/direct-ip", log, func() []string { return nil })
	c.mu.Lock()
	c.tunnelIP = "31.77.17.245"
	c.mu.Unlock()

	c.maybeBaseline("31.77.17.245") // refused
	c.maybeBaseline("79.139.199.79")

	c.mu.Lock()
	got := c.directIP
	c.mu.Unlock()
	if got != "79.139.199.79" {
		t.Fatalf("baseline = %q, want 79.139.199.79", got)
	}
	if n := log.count("baseline ISP IP updated"); n != 1 {
		t.Errorf("baseline update logged %d times, want 1", n)
	}

	// And a return to the refused address logs again, since the run of
	// identical refusals was broken by the successful adoption.
	c.maybeBaseline("31.77.17.245")
	if n := log.count("refusing to set baseline"); n != 2 {
		t.Errorf("refusals logged %d times, want 2", n)
	}
}

// A refusal that keeps standing must reappear in the log every so often: a log
// that has rotated away its early lines should still say the guard is active.
func TestBaselineRefusalRepeatsAfterTheInterval(t *testing.T) {
	log := &countingLogger{}
	c := New(t.TempDir()+"/direct-ip", log, func() []string { return nil })
	c.mu.Lock()
	c.tunnelIP = "31.77.17.245"
	c.mu.Unlock()

	for i := 0; i < 150; i++ { // ~5 minutes of a 2s probe loop
		c.maybeBaseline("31.77.17.245")
	}
	if got := log.count("refusing to set baseline"); got != 1 {
		t.Fatalf("logged %d lines before the interval elapsed, want 1", got)
	}

	// Backdate the last line past the interval, as the clock would.
	c.mu.Lock()
	c.lastRefusedBaselineAt = time.Now().Add(-refusedBaselineInterval - time.Second)
	c.mu.Unlock()

	c.maybeBaseline("31.77.17.245")
	if got := log.count("refusing to set baseline"); got != 2 {
		t.Fatalf("logged %d lines after the interval elapsed, want 2", got)
	}

	// The periodic line has to carry the count of what it stood in for,
	// otherwise it reads as a single event rather than an ongoing state.
	var periodic string
	for _, l := range log.lines {
		if strings.Contains(l, "still refusing") {
			periodic = l
		}
	}
	if periodic == "" {
		t.Fatal("no periodic line was written")
	}
	if !strings.Contains(periodic, "probes suppressed") {
		t.Errorf("periodic line %q does not report how many probes it stands for", periodic)
	}
	if strings.Contains(periodic, "0 probes") {
		t.Errorf("periodic line %q reports zero suppressed probes", periodic)
	}

	// And it goes quiet again for another interval.
	for i := 0; i < 50; i++ {
		c.maybeBaseline("31.77.17.245")
	}
	if got := log.count("refusing to set baseline"); got != 2 {
		t.Fatalf("logged %d lines, want the counter to stay at 2", got)
	}
}
