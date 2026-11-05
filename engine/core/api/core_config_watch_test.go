package api

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"NekkoVPN/core/engine"
)

type staleCore struct {
	stale atomic.Bool
	asked atomic.Int32
}

func (s *staleCore) CoreConfigStale() bool {
	s.asked.Add(1)
	return s.stale.Load()
}

// The core left on the previous release's config restarts without the user, and
// a core on the current one is left alone.
func TestACoreOnAnOlderConfigIsReconnected(t *testing.T) {
	interval := coreConfigCheckInterval
	coreConfigCheckInterval = 5 * time.Millisecond
	defer func() { coreConfigCheckInterval = interval }()

	f := &alternateEngine{caps: engine.Capabilities{TUN: true, DomainRouting: true, IPRouting: true}}
	c := initAlternate(t, f)
	if _, err := c.AddProfileFromURI("p-first"); err != nil {
		t.Fatal(err)
	}
	if err := c.StartVPN(); err != nil {
		t.Fatal(err)
	}
	starts := func() int {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.starts
	}
	before := starts()

	core := &staleCore{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); c.watchCoreConfig(ctx, core) }()
	defer func() { cancel(); <-done }()

	for core.asked.Load() < 3 {
		time.Sleep(time.Millisecond)
	}
	if got := starts(); got != before {
		t.Fatalf("restarted a current core: %d starts, was %d", got, before)
	}

	core.stale.Store(true)
	deadline := time.Now().Add(5 * time.Second)
	for starts() == before {
		if time.Now().After(deadline) {
			t.Fatal("no reconnect on a stale config")
		}
		time.Sleep(time.Millisecond)
	}
	core.stale.Store(false)
}
