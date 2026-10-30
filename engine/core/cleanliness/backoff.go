package cleanliness

import (
	"math/rand"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// probeBackoff throttles one external source after it fails.
//
// The sources here are free public APIs with per-minute quotas, and the
// refresh loop asks them for the same answer every RefreshInterval whether or
// not the last answer arrived. When a source starts refusing — ip-api.com
// closes the connection outright (EOF) once its quota is spent — that turns
// into a failure every 20 seconds, which both fills the log with identical
// lines and keeps spending requests the quota window is trying to protect.
//
// Backing off is the fix, and it has to be per-source: ip-api being angry
// says nothing about ipapi.is, and one shared timer would punish a healthy
// source for a sick one.
//
// The delay doubles per consecutive failure from backoffBase to backoffMax,
// with jitter so several clients that failed together do not come back in
// lockstep. A success clears it immediately — this exists to be polite about
// repeated failure, not to add latency to a working source.
type probeBackoff struct {
	name string

	mu       sync.Mutex
	failures int
	nextTry  time.Time
	// lastReason is kept so the periodic "still backing off" line can say
	// what the original failure was, long after it scrolled away.
	lastReason string
}

const (
	// backoffBase is the first delay, one RefreshInterval: a single failure
	// costs exactly one skipped round, no more.
	backoffBase = RefreshInterval
	// backoffMax caps the delay. Ten minutes is short enough that a source
	// recovering during a session gets picked back up on its own, and long
	// enough that a source that is down for the whole session costs a handful
	// of requests rather than a hundred.
	backoffMax = 10 * time.Minute
)

func newProbeBackoff(name string) *probeBackoff {
	return &probeBackoff{name: name}
}

// ready reports whether the source may be queried now. When it returns false
// it also returns how long is left, for the caller's log line.
func (b *probeBackoff) ready() (bool, time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.nextTry.IsZero() {
		return true, 0
	}
	if left := time.Until(b.nextTry); left > 0 {
		return false, left
	}
	return true, 0
}

// fail records a failed attempt and schedules the next one. retryAfter, when
// non-zero, is the delay the source itself asked for (ip-api's X-Ttl header)
// and wins over the computed delay — a server saying "not for 47 seconds" is
// better information than any local guess.
func (b *probeBackoff) fail(reason string, retryAfter time.Duration) time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	b.lastReason = reason

	var delay time.Duration
	if retryAfter > 0 {
		// The server's own number, honoured as given: no jitter on top of it,
		// since it describes a real window rather than a guess.
		delay = retryAfter
	} else {
		// Shift with a bounded exponent — 1<<64 wraps to zero, and a source
		// that has failed sixty times must land on the cap, not on no wait.
		delay = backoffBase << min(b.failures-1, 16)
		if delay <= 0 {
			delay = backoffMax
		}
		// ±20% jitter, so several clients that failed together do not all
		// come back in the same instant. Applied before the clamp below, or
		// the positive half of the jitter would push past the cap.
		if span := int64(delay / 5); span > 0 {
			delay += time.Duration(rand.Int63n(span*2+1)) - delay/5
		}
		if delay > backoffMax {
			delay = backoffMax
		}
	}
	// Never below one refresh round, whichever branch produced the number:
	// a "retry after 1s" header would otherwise reinstate the hammering.
	if delay < backoffBase {
		delay = backoffBase
	}
	b.nextTry = time.Now().Add(delay)
	return delay
}

// succeed clears the backoff. Safe to call when there was none.
func (b *probeBackoff) succeed() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
	b.nextTry = time.Time{}
	b.lastReason = ""
}

// state is a snapshot for logging.
func (b *probeBackoff) state() (failures int, reason string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.failures, b.lastReason
}

// retryAfterFromResponse reads the delay a source asked for. ip-api.com sends
// X-Ttl (seconds until its per-minute window resets) alongside a 429; the
// standard Retry-After is handled too, since ipapi.is and IPQS use it.
func retryAfterFromResponse(resp *http.Response) time.Duration {
	if resp == nil {
		return 0
	}
	for _, h := range []string{"X-Ttl", "Retry-After"} {
		v := resp.Header.Get(h)
		if v == "" {
			continue
		}
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
		// Retry-After may also be an HTTP date.
		if t, err := http.ParseTime(v); err == nil {
			if d := time.Until(t); d > 0 {
				return d
			}
		}
	}
	return 0
}
