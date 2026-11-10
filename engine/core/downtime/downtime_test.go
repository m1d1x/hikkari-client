package downtime

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// rig drives a meter with a fake clock and a scripted core.
type rig struct {
	t        *testing.T
	m        *Meter
	clock    time.Time
	wanted   bool
	running  bool
	conns    []Conn
	internet bool
	probes   int
	lines    []string
}

func newRig(t *testing.T) *rig {
	r := &rig{t: t, wanted: true, running: true, internet: true}
	// A minute boundary, so twelve looks make one minute.
	r.clock = time.Unix(1_800_000_000/60*60, 0)
	r.m = New(Sources{
		Tunnel: func() (bool, bool) { return r.wanted, r.running },
		Conns:  func() ([]Conn, error) { return r.conns, nil },
		Internet: func(context.Context) (bool, bool) {
			r.probes++
			return r.internet, true
		},
		Logf: func(format string, args ...any) { r.lines = append(r.lines, fmt.Sprintf(format, args...)) },
	})
	r.m.now = func() time.Time { return r.clock }
	return r
}

// minute polls through one minute, letting each look change the core first.
func (r *rig) minute(each func(i int)) {
	for i := 0; i < 12; i++ {
		if each != nil {
			each(i)
		}
		r.m.Poll(context.Background())
		r.clock = r.clock.Add(pollEvery)
	}
}

// states closes the open minute and returns every state written.
func (r *rig) states() string {
	r.wanted = false
	r.m.Poll(context.Background())
	var b strings.Builder
	for _, l := range r.lines {
		_, s, _ := strings.Cut(l, "states=")
		b.WriteString(s)
	}
	return b.String()
}

// asks opens a connection after the first look that sends a hello and never
// hears back.
func asks(r *rig) func(int) {
	var start time.Time
	return func(i int) {
		if i == 1 {
			start = r.clock.Add(-time.Second)
		}
		if i >= 1 {
			r.conns = []Conn{{ID: "a", Up: 600, Start: start}}
		}
	}
}

func TestAnswerMakesMinuteFlowing(t *testing.T) {
	r := newRig(t)
	r.minute(func(i int) {
		r.conns = []Conn{{ID: "a", Up: uint64(500 * (i + 1)), Down: uint64(100 * i), Start: r.clock}}
	})
	if got := r.states(); got != "o" {
		t.Fatalf("states %q, want o", got)
	}
}

func TestQuestionWithoutAnswerIsDown(t *testing.T) {
	r := newRig(t)
	r.minute(asks(r))
	if got := r.states(); got != "d" {
		t.Fatalf("states %q, want d", got)
	}
	if r.probes != 1 {
		t.Fatalf("direct path probed %d times, want once", r.probes)
	}
}

func TestUsersOwnNetworkIsNotOurs(t *testing.T) {
	r := newRig(t)
	r.internet = false
	r.minute(asks(r))
	if got := r.states(); got != "n" {
		t.Fatalf("states %q, want n", got)
	}
}

func TestIdleTunnelIsNotDown(t *testing.T) {
	r := newRig(t)
	// A long-lived connection that sent long ago and waits quietly.
	r.conns = []Conn{{ID: "ws", Up: 5000, Down: 5000}}
	r.minute(nil)
	if got := r.states(); got != "i" {
		t.Fatalf("states %q, want i", got)
	}
	if r.probes != 0 {
		t.Fatalf("an idle minute probed the direct path")
	}
}

func TestDirectTrafficSaysNothingAboutTheTunnel(t *testing.T) {
	r := newRig(t)
	r.minute(func(i int) {
		r.conns = []Conn{
			{ID: "ru", Up: uint64(500 * (i + 1)), Down: uint64(900 * (i + 1)), Direct: true},
		}
		if i >= 1 {
			r.conns = append(r.conns, Conn{ID: "a", Up: 600, Start: r.m.lastPoll.Add(time.Second)})
		}
	})
	if got := r.states(); got != "d" {
		t.Fatalf("states %q, want d", got)
	}
}

func TestShortConnectionBetweenLooksCounts(t *testing.T) {
	r := newRig(t)
	r.minute(func(i int) {
		r.conns = nil
		if i == 6 {
			r.conns = []Conn{{ID: "short", Up: 700, Down: 3000, Start: r.clock.Add(-2 * time.Second)}}
		}
	})
	if got := r.states(); got != "o" {
		t.Fatalf("states %q, want o", got)
	}
}

func TestLongSleepStartsANewRun(t *testing.T) {
	r := newRig(t)
	r.minute(nil)
	r.clock = r.clock.Add(time.Hour)
	r.minute(nil)
	if got := r.states(); got != "ii" || len(r.lines) != 2 {
		t.Fatalf("states %q in %d lines, want ii in 2", got, len(r.lines))
	}
}

func TestKeepaliveIsNotAQuestion(t *testing.T) {
	r := newRig(t)
	r.minute(func(i int) { r.conns = []Conn{{ID: "a", Up: uint64(10 * i)}} })
	if got := r.states(); got != "i" {
		t.Fatalf("states %q, want i", got)
	}
}

func TestAnswerLateInTheWaitIsNotDown(t *testing.T) {
	r := newRig(t)
	// The hello goes out on the last look; the next minute brings the answer.
	r.minute(func(i int) {
		if i == 11 {
			r.conns = []Conn{{ID: "a", Up: 600}}
		}
	})
	r.minute(func(i int) { r.conns = []Conn{{ID: "a", Up: 600, Down: 4000}} })
	if got := r.states(); got != "io" {
		t.Fatalf("states %q, want io", got)
	}
}

func TestCoreDownWhileWanted(t *testing.T) {
	r := newRig(t)
	r.running = false
	r.minute(nil)
	if got := r.states(); got != "c" {
		t.Fatalf("states %q, want c", got)
	}
	if !CoreDown.Lost() || !Down.Lost() || NetDown.Lost() || Idle.Lost() {
		t.Fatal("only d and c count towards the metric")
	}
}

func TestTunnelOffWritesNothing(t *testing.T) {
	r := newRig(t)
	r.wanted = false
	r.minute(nil)
	if got := r.states(); got != "" {
		t.Fatalf("states %q, want none", got)
	}
}

func TestHalfMinuteIsNotCounted(t *testing.T) {
	r := newRig(t)
	r.wanted = false
	r.minute(func(i int) { r.wanted = i >= 8 }) // connected for the last 4 looks
	r.minute(nil)
	if got := r.states(); got != "i" {
		t.Fatalf("states %q, want i", got)
	}
}

func TestSleepLeavesUnseenMinutes(t *testing.T) {
	r := newRig(t)
	r.minute(nil)
	r.clock = r.clock.Add(2 * time.Minute)
	r.minute(nil)
	if got := r.states(); got != "i--i" {
		t.Fatalf("states %q, want i--i", got)
	}
	if !strings.HasPrefix(r.lines[0], fmt.Sprintf("[downtime] minutes from=%d ", time.Unix(1_800_000_000/60*60, 0).Unix())) {
		t.Fatalf("line %q", r.lines[0])
	}
}

func TestFlushEveryTenMinutes(t *testing.T) {
	r := newRig(t)
	for i := 0; i < 12; i++ {
		r.minute(nil)
	}
	if len(r.lines) == 0 {
		t.Fatal("nothing written after twelve minutes")
	}
	if got := r.states(); got != strings.Repeat("i", 12) {
		t.Fatalf("states %q", got)
	}
}
