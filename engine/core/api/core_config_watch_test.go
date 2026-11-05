package api

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"NekkoVPN/core/engine"
)

// superviseEngine is an engine whose core lives apart from the GUI: a service the
// test answers for.
type superviseEngine struct {
	*alternateEngine
	mu        sync.Mutex
	state     engine.CoreState
	stateErr  error
	startErr  error
	asked     int
	restarts  int
	userStops int
}

func (s *superviseEngine) CoreState() (engine.CoreState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asked++
	return s.state, s.stateErr
}

func (s *superviseEngine) StopForRestart() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.restarts++
	s.state.Running = false
	s.mu.Unlock()
	defer s.mu.Lock()
	return s.alternateEngine.Stop()
}

func (s *superviseEngine) Stop() error {
	s.mu.Lock()
	s.userStops++
	s.state.Running, s.state.Wanted = false, false
	s.mu.Unlock()
	return s.alternateEngine.Stop()
}

func (s *superviseEngine) Start(ctx context.Context) error {
	s.mu.Lock()
	err := s.startErr
	if err == nil {
		s.state.Running, s.state.Wanted, s.state.ConfigCurrent = true, true, true
	}
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return s.alternateEngine.Start(ctx)
}

func (s *superviseEngine) set(f func(*superviseEngine)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(s)
}

// initSupervised gives a Core with a profile, without its own supervisor running:
// the tests step superviseCoreOnce themselves.
func initSupervised(t *testing.T) (*Core, *superviseEngine) {
	t.Helper()
	interval := coreSuperviseInterval
	coreSuperviseInterval = time.Hour
	t.Cleanup(func() { coreSuperviseInterval = interval })
	f := &superviseEngine{alternateEngine: &alternateEngine{caps: engine.Capabilities{TUN: true, DomainRouting: true, IPRouting: true}}}
	c := New()
	if err := c.InitWithEngine(manualRoutingDir(t), func(string, engine.Logger) (engine.Backend, error) { return f, nil }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Shutdown() })
	if _, err := c.AddProfileFromURI("p-first"); err != nil {
		t.Fatal(err)
	}
	return c, f
}

func (s *superviseEngine) startCount() int {
	s.alternateEngine.mu.Lock()
	defer s.alternateEngine.mu.Unlock()
	return s.alternateEngine.starts
}

// After an update the core waits for this GUI: one start, the first thing the GUI
// does, and nothing more afterwards.
func TestAWantedCoreDownIsStartedOnce(t *testing.T) {
	c, f := initSupervised(t)
	f.set(func(f *superviseEngine) { f.state = engine.CoreState{Supervised: true, Wanted: true} })
	watch := coreWatch{}
	before := f.startCount()
	for range 3 {
		c.superviseCoreOnce(context.Background(), f, &watch)
	}
	if got := f.startCount() - before; got != 1 {
		t.Fatalf("%d starts, want 1", got)
	}
	if f.restarts != 0 {
		t.Fatal("a core started on this release's config was restarted")
	}
}

// A failed connect is tried again, with a pause that grows and stays bounded, and
// a success ends it.
func TestAFailedConnectIsRetried(t *testing.T) {
	c, f := initSupervised(t)
	interval := coreSuperviseInterval
	coreSuperviseInterval = 5 * time.Second
	defer func() { coreSuperviseInterval = interval }()
	f.set(func(f *superviseEngine) {
		f.state = engine.CoreState{Supervised: true, Wanted: true}
		f.startErr = errors.New("no network")
	})
	watch := coreWatch{}
	c.superviseCoreOnce(context.Background(), f, &watch)
	if watch.failures != 1 || !watch.retryAt.After(time.Now()) {
		t.Fatalf("after a failure: %+v", watch)
	}
	first := time.Until(watch.retryAt)
	before := f.startCount()
	c.superviseCoreOnce(context.Background(), f, &watch)
	if watch.failures != 1 {
		t.Fatal("tried again before the pause ended")
	}
	for range 12 {
		watch.retryAt = time.Time{}
		c.superviseCoreOnce(context.Background(), f, &watch)
	}
	if pause := time.Until(watch.retryAt); pause <= first || pause > coreRetryMax {
		t.Fatalf("pause %s after %d failures", pause, watch.failures)
	}
	f.set(func(f *superviseEngine) { f.startErr = nil })
	watch.retryAt = time.Time{}
	c.superviseCoreOnce(context.Background(), f, &watch)
	if f.startCount() != before+1 {
		t.Fatal("no start once the failure was gone")
	}
	if watch.failures != 0 {
		t.Fatalf("failures left after a success: %+v", watch)
	}
}

// A core this GUI finds on another release's config restarts once; one on its own
// is left alone, and a later difference never breaks the tunnel.
func TestACoreOnAnotherConfigIsReconnectedOnce(t *testing.T) {
	c, f := initSupervised(t)
	f.set(func(f *superviseEngine) { f.state = engine.CoreState{Supervised: true, Wanted: true, Running: true} })
	watch := coreWatch{}
	before := f.startCount()
	c.superviseCoreOnce(context.Background(), f, &watch)
	if f.restarts != 1 || f.startCount() != before+1 {
		t.Fatalf("restarts %d, starts %d", f.restarts, f.startCount()-before)
	}
	if f.userStops != 0 {
		t.Fatal("a reconnect ended the wish to be up")
	}
	f.set(func(f *superviseEngine) { f.state.ConfigCurrent = false })
	c.superviseCoreOnce(context.Background(), f, &watch)
	if f.restarts != 1 {
		t.Fatal("a config that changed later broke the tunnel")
	}

	watch = coreWatch{}
	f.set(func(f *superviseEngine) { f.state.ConfigCurrent = true })
	c.superviseCoreOnce(context.Background(), f, &watch)
	if f.restarts != 1 {
		t.Fatal("restarted a core on its own config")
	}
}

// Disconnect wins: what the user asked for is read under the same lock as the
// action, and a core nobody wants stays down, config check included.
func TestSupervisorLeavesAStoppedCoreAlone(t *testing.T) {
	c, f := initSupervised(t)
	f.set(func(f *superviseEngine) { f.state = engine.CoreState{Supervised: true, Wanted: true} })
	if err := c.StopVPN(); err != nil {
		t.Fatal(err)
	}
	watch := coreWatch{}
	before := f.startCount()
	c.superviseCoreOnce(context.Background(), f, &watch)
	if f.startCount() != before {
		t.Fatal("the core came back after a disconnect")
	}
	// The next connect sends the config: nothing is left to compare.
	if !watch.configChecked {
		t.Fatal("a stopped core left the config check pending")
	}
}

// The previous service, kept for a moment by an update, reports nothing to act on.
func TestSupervisorWaitsOutAServiceThatDoesNotReport(t *testing.T) {
	c, f := initSupervised(t)
	f.set(func(f *superviseEngine) { f.state = engine.CoreState{Wanted: true} })
	watch := coreWatch{}
	before := f.startCount()
	c.superviseCoreOnce(context.Background(), f, &watch)
	f.set(func(f *superviseEngine) { f.stateErr = errors.New("no service") })
	c.superviseCoreOnce(context.Background(), f, &watch)
	if f.startCount() != before || watch.configChecked {
		t.Fatalf("acted on an old service: %+v", watch)
	}
}

// A press that waited for the supervisor finds the core up and does not restart it.
func TestStartVPNOnARunningCoreIsNoBreak(t *testing.T) {
	c, f := initSupervised(t)
	f.alternateEngine.mu.Lock()
	f.alternateEngine.running = true
	f.alternateEngine.mu.Unlock()
	before := f.startCount()
	if err := c.StartVPN(); err != nil {
		t.Fatal(err)
	}
	if f.startCount() != before {
		t.Fatal("started a running core")
	}
}
