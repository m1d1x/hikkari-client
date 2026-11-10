package api

import (
	"context"
	"time"

	"NekkoVPN/core/downtime"
	"NekkoVPN/core/engine"
	"NekkoVPN/core/netdoctor"
	"NekkoVPN/core/rureach"
)

// downtimeMeter counts the minutes the tunnel is on and carries nothing
// (core/downtime), nil when the engine cannot list its connections.
func (c *Core) downtimeMeter() *downtime.Meter {
	traffic, ok := c.backend.(engine.DirectTraffic)
	if !ok {
		return nil
	}
	return downtime.New(downtime.Sources{
		Tunnel: c.tunnelWanted,
		Conns: func() ([]downtime.Conn, error) {
			conns, err := traffic.DirectConnections()
			if err != nil {
				return nil, err
			}
			out := make([]downtime.Conn, len(conns))
			for i, conn := range conns {
				out[i] = downtime.Conn{ID: conn.ID, Up: conn.Upload, Down: conn.Download, Direct: conn.Direct, Start: conn.Start}
			}
			return out, nil
		},
		Internet: c.directInternet,
		Logf:     func(format string, args ...any) { c.logger.Logf(format, args...) },
	})
}

// tunnelWanted is the user's switch and whether the core answers. A core kept
// by a service knows the switch apart from the core; elsewhere the two are one.
func (c *Core) tunnelWanted() (wanted, running bool) {
	running = c.backend.IsRunning()
	if core, ok := c.backend.(engine.CoreSupervision); ok {
		if state, err := core.CoreState(); err == nil && state.Supervised {
			return state.Wanted, state.Running && running
		}
	}
	return running, running
}

// directInternet asks ordinary sites over the path bypassed apps use: through
// the core while it runs (everything from this process goes into the TUN),
// straight from the machine while it does not.
func (c *Core) directInternet(ctx context.Context) (ok, checked bool) {
	if ctx.Err() != nil {
		return false, false
	}
	if !c.backend.IsRunning() {
		return netdoctor.DirectInternet(netdoctor.FetchDirect), true
	}
	prober, can := c.backend.(engine.DirectProber)
	if !can || !prober.CanProbeDirect() {
		return false, false
	}
	timeout := int(netdoctor.DirectProbeTimeout / time.Millisecond)
	return netdoctor.DirectInternet(func(url string) error { return prober.ProbeDirect(url, timeout) }), true
}

// ruReachProber measures a tester's own network to Russian sites
// (core/rureach), next to the downtime minutes it explains.
func (c *Core) ruReachProber() *rureach.Prober {
	presetManager := c.presets
	return rureach.New(rureach.Sources{
		Enabled: func() bool { return presetManager.Snapshot().Tester },
		Fetch:   c.fetchDirectTimed,
		Logf:    func(format string, args ...any) { c.logger.Logf(format, args...) },
	})
}

// fetchDirectTimed asks url over the direct path and times the answer:
// through the core while it runs, straight from the machine while it does not.
func (c *Core) fetchDirectTimed(ctx context.Context, url string) (int, bool, error) {
	if ctx.Err() != nil {
		return 0, false, ctx.Err()
	}
	if !c.backend.IsRunning() {
		start := time.Now()
		err := netdoctor.FetchDirect(url)
		return int(time.Since(start) / time.Millisecond), true, err
	}
	timer, ok := c.backend.(engine.DirectTimer)
	prober, can := c.backend.(engine.DirectProber)
	if !ok || !can || !prober.CanProbeDirect() {
		return 0, false, nil
	}
	ms, err := timer.DelayDirect(url, int(rureach.Timeout/time.Millisecond))
	return ms, true, err
}
