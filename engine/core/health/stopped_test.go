package health

import (
	"testing"
	"time"
)

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

// A change of rules restarts the core in a few seconds, faster than the pass
// loop can see it down. Seen in the field: the probe right after such a restart
// left through the ISP before the new TUN routes were in, and the window said
// "трафик уходит мимо туннеля". The new core gets the start grace, and the
// window keeps "ok" through it instead of flashing "connecting".
func TestARestartGetsTheStartGraceWithoutLeavingOK(t *testing.T) {
	c := New(t.TempDir()+"/ip", nopLogger{}, nil, func() bool { return true })
	c.directIP = "203.0.113.9"
	ok := Info{Running: true, TunnelOK: true, Status: "ok", IP: "198.51.100.7"}
	c.cached = ok
	c.lastRunning = true
	c.upSince = time.Now().Add(-time.Hour)
	c.lastEgressAt = time.Now()

	c.MarkRestarting()
	c.refresh()

	if got := c.Probe(); got != ok {
		t.Fatalf("restart within the grace: %+v", got)
	}
	if time.Since(c.upSince) > time.Second {
		t.Fatalf("upSince not reset by the restart: %s ago", time.Since(c.upSince))
	}
	if c.restarting {
		t.Fatal("the restart was seen through but is still pending")
	}
}

// A first start is not a restart: it shows "connecting" through the grace.
func TestAFirstStartStillShowsConnecting(t *testing.T) {
	c := New(t.TempDir()+"/ip", nopLogger{}, nil, func() bool { return true })
	c.cached = Info{Status: "off", Message: offMessage}
	c.refresh()
	if got := c.Probe(); got.Status != "connecting" {
		t.Fatalf("first start: %+v", got)
	}
}
