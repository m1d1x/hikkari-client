package uptime

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type countingProbe struct {
	mu    sync.Mutex
	calls map[string]int
}

func (p *countingProbe) Run(_ context.Context, h HostEntry) (bool, int, Source) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.calls == nil {
		p.calls = map[string]int{}
	}
	p.calls[h.ProfileID]++
	return true, 42, SrcTunnel
}

func (p *countingProbe) count(id string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls[id]
}

func (p *countingProbe) total() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, c := range p.calls {
		n += c
	}
	return n
}

var threeHosts = []HostEntry{
	{Profile: "a", ProfileID: "a"},
	{Profile: "b", ProfileID: "b"},
	{Profile: "c", ProfileID: "c"},
}

func hostsFn() []HostEntry { return threeHosts }

func runTracker(t *testing.T, tr *Tracker, active ActiveProvider, probe Probe, observer Observer) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); tr.Run(ctx, hostsFn, active, probe, observer) }()
	t.Cleanup(func() { cancel(); <-done })
}

func newTracker(t *testing.T) *Tracker {
	t.Helper()
	return New(filepath.Join(t.TempDir(), "uptime.json"))
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestEngineOffListClosedSendsNothing(t *testing.T) {
	probe := &countingProbe{}
	runTracker(t, newTracker(t), func() string { return "" }, probe, nil)
	time.Sleep(100 * time.Millisecond)
	if n := probe.total(); n != 0 {
		t.Fatalf("%d probes with nothing to watch: bare TCP to foreign servers on a timer", n)
	}
}

func TestListClosedProbesOnlyActiveProfile(t *testing.T) {
	probe := &countingProbe{}
	runTracker(t, newTracker(t), func() string { return "b" }, probe, nil)
	waitFor(t, "the active probe", func() bool { return probe.count("b") == 1 })
	time.Sleep(50 * time.Millisecond)
	if probe.total() != 1 {
		t.Fatalf("calls = %v, want one probe of b", probe.calls)
	}
}

func TestOpeningListSweepsAllAndClosingStops(t *testing.T) {
	tr := newTracker(t)
	probe := &countingProbe{}
	runTracker(t, tr, func() string { return "" }, probe, nil)
	tr.SetUIPresence(true)
	waitFor(t, "the sweep", func() bool { return probe.total() == 3 })
	tr.SetUIPresence(false)
	time.Sleep(50 * time.Millisecond)
	if n := probe.total(); n != 3 {
		t.Fatalf("%d probes after closing the list", n)
	}
	if got := tr.Snapshot(); len(got["a"]) != 1 || len(got["c"]) != 1 {
		t.Fatalf("history = %v", got)
	}
}

func TestReopeningListDoesNotSweepAgainImmediately(t *testing.T) {
	tr := newTracker(t)
	probe := &countingProbe{}
	runTracker(t, tr, func() string { return "" }, probe, nil)
	tr.SetUIPresence(true)
	waitFor(t, "the sweep", func() bool { return probe.total() == 3 })
	for range 5 {
		tr.SetUIPresence(false)
		tr.SetUIPresence(true)
		time.Sleep(10 * time.Millisecond)
	}
	if n := probe.total(); n != 3 {
		t.Fatalf("%d probes from flicking the list", n)
	}
}

func TestChangeOfActiveProfileProbesItAtOnce(t *testing.T) {
	tr := newTracker(t)
	probe := &countingProbe{}
	var active atomic.Value
	active.Store("a")
	runTracker(t, tr, func() string { return active.Load().(string) }, probe, nil)
	waitFor(t, "the first profile", func() bool { return probe.count("a") == 1 })
	active.Store("c")
	tr.Nudge()
	waitFor(t, "the new profile", func() bool { return probe.count("c") == 1 })
}

type fakeObserver struct {
	received func() map[string]uint64
	own      func() (bool, int, bool)
}

func (o fakeObserver) Received(string) (map[string]uint64, error) { return o.received(), nil }
func (o fakeObserver) OwnProbe(string) (bool, int, bool) {
	if o.own == nil {
		return false, 0, false
	}
	return o.own()
}

func TestOpeningListInterruptsTrafficWindow(t *testing.T) {
	tr := newTracker(t)
	probe := &countingProbe{}
	var reads atomic.Int32
	observer := fakeObserver{received: func() map[string]uint64 { reads.Add(1); return nil }}
	runTracker(t, tr, func() string { return "a" }, probe, observer)
	waitFor(t, "the traffic window", func() bool { return reads.Load() == 1 })
	tr.SetUIPresence(true)
	waitFor(t, "a sweep well before the window ends", func() bool { return probe.total() == 3 })
}

func TestSkipProbeSeesOnlyGrowth(t *testing.T) {
	tr := newTracker(t)
	cases := []struct {
		name          string
		before, after map[string]uint64
		want          bool
	}{
		{"stalled stream", map[string]uint64{"x": 100}, map[string]uint64{"x": 100}, false},
		{"bytes arrived", map[string]uint64{"x": 100}, map[string]uint64{"x": 200}, true},
		{"new connection answered", nil, map[string]uint64{"y": 10}, true},
		{"nothing open", nil, nil, false},
	}
	if testing.Short() {
		t.Skip("each case waits out the real traffic window")
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var local atomic.Int32
			values := []map[string]uint64{c.before, c.after}
			observer := fakeObserver{received: func() map[string]uint64 { return values[local.Add(1)-1] }}
			if got := tr.skipProbe(context.Background(), observer, "a"); got != c.want {
				t.Fatalf("skipProbe = %v, want %v", got, c.want)
			}
		})
	}
}

func TestOwnProbeSamplesWithoutNetwork(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out the real traffic window")
	}
	tr := newTracker(t)
	probe := &countingProbe{}
	observer := fakeObserver{
		received: func() map[string]uint64 { return nil },
		own:      func() (bool, int, bool) { return true, 77, true },
	}
	tr.checkActive(context.Background(), threeHosts[0], probe, observer)
	if probe.total() != 0 {
		t.Fatalf("probed although the core had measured: %v", probe.calls)
	}
	got := tr.Snapshot()["a"]
	if len(got) != 1 || got[0].RttMs != 77 || got[0].Src != SrcTunnel {
		t.Fatalf("samples = %+v", got)
	}
}
