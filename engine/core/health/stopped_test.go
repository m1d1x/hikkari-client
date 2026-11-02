package health

import "testing"

type nopLogger struct{}

func (nopLogger) Logf(string, ...interface{}) {}

func TestMarkStoppedIsVisibleAtOnce(t *testing.T) {
	c := New(t.TempDir()+"/ip", nopLogger{}, nil, func() bool { return true })
	c.cached = Info{Running: true, Status: "ok", IP: "198.51.100.7"}

	c.MarkStopped()

	got := c.Probe()
	if got.Running || got.Status != "off" || got.IP != "" {
		t.Fatalf("after a stop: %+v", got)
	}
	if c.downStreak != downStreakThreshold {
		t.Errorf("the next pass must believe the core is down: streak %d", c.downStreak)
	}
}

func TestAPassFromBeforeTheStopDoesNotUndoIt(t *testing.T) {
	stop := make(chan struct{})
	c := New(t.TempDir()+"/ip", nopLogger{}, nil, func() bool {
		close(stop)
		return true // the core is still up when this pass looks at it
	})
	c.cached = Info{Running: true, Status: "ok"}
	c.lastRunning = true
	done := make(chan struct{})
	go func() { c.refresh(); close(done) }()
	<-stop
	c.MarkStopped()
	<-done
	if got := c.Probe(); got.Status != "off" {
		t.Fatalf("stale pass overwrote the stop: %+v", got)
	}
}
