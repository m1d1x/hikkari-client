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
	// stalls are the core's totals; nil is a core that does not time them.
	stalls *StallTotals
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
		Stalls: func() (StallTotals, bool) {
			if r.stalls == nil {
				return StallTotals{}, false
			}
			return *r.stalls, true
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

func flowing(r *rig) func(int) { return flowingWith(r, nil) }

// flowingWith is a flowing minute in which the core's totals become stalls
// halfway through.
func flowingWith(r *rig, stalls *StallTotals) func(int) {
	return func(i int) {
		r.conns = []Conn{{ID: "a", Up: uint64(1000 + i), Down: uint64(1000 + i)}}
		if i == 6 && stalls != nil {
			r.stalls = stalls
		}
	}
}

// stallLines closes the run and returns its stalls lines.
func (r *rig) stallLines() []string {
	r.states()
	var out []string
	for _, l := range r.lines {
		if strings.HasPrefix(l, "[downtime] stalls ") {
			out = append(out, l)
		}
	}
	return out
}

func TestEachMinuteCarriesTheStallsTheCoreTimedInIt(t *testing.T) {
	r := newRig(t)
	r.stalls = &StallTotals{BoundsMS: []int64{300, 1000, 3000}, Count: []int64{5, 1, 0}, MS: []int64{4000, 1500, 0}}
	r.minute(flowing(r)) // the first minute of a core has no base
	r.minute(flowing(r)) // nothing new
	r.minute(flowingWith(r, &StallTotals{BoundsMS: []int64{300, 1000, 3000}, Count: []int64{7, 2, 0}, MS: []int64{5600, 2700, 0}}))
	from := r.clock.Unix()/60*60 - 180
	want := fmt.Sprintf("[downtime] stalls from=%d bounds=300,1000,3000 v=-;.;2:1600,1:1200,0:0", from)
	if got := r.stallLines(); len(got) != 1 || got[0] != want {
		t.Fatalf("want %q, got %q", want, got)
	}
}

func TestAnOlderCoreWritesNoStallsLine(t *testing.T) {
	r := newRig(t)
	r.minute(flowing(r))
	r.minute(flowing(r))
	if got := r.stallLines(); len(got) != 0 {
		t.Fatalf("a core without stalls wrote %q", got)
	}
}

func TestARestartedCoreStartsItsStallsOver(t *testing.T) {
	r := newRig(t)
	r.stalls = &StallTotals{BoundsMS: []int64{300, 1000, 3000}, Count: []int64{9, 3, 1}, MS: []int64{9000, 6000, 3100}}
	r.minute(flowing(r))
	r.minute(flowing(r))
	// The core came back with its totals from zero: not a minute of minus nine.
	r.minute(flowingWith(r, &StallTotals{BoundsMS: []int64{300, 1000, 3000}, Count: []int64{1, 0, 0}, MS: []int64{400, 0, 0}}))
	r.minute(flowingWith(r, &StallTotals{BoundsMS: []int64{300, 1000, 3000}, Count: []int64{2, 0, 0}, MS: []int64{750, 0, 0}}))
	got := r.stallLines()
	if len(got) != 1 || !strings.HasSuffix(got[0], " v=-;.;-;1:350,0:0,0:0") {
		t.Fatalf("got %q", got)
	}
}
