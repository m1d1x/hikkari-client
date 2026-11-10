// Package rureach measures a tester's own network to Russia: once a minute,
// ordinary Russian sites over the path bypassed apps use, never through the
// tunnel. It is what tells a broken tunnel from a broken home network on the
// testers' screen of the admin (plans/ROADMAP.md: testers are observed in
// full, ordinary users are not), so it runs only in tester mode.
//
// A minute is the fastest answer in milliseconds, "x" when no site answered,
// "-" when it could not be measured (the core running and not answering, the
// machine asleep). The minutes go to the log every flushEvery as
// "[rureach] minutes from=<unix> ms=31,29,x,-,30"; the hub reads them from
// testers' logs.
package rureach

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Sites are Russian on purpose: hosted in Russian data centres, answered over
// the user's provider without crossing the border.
var Sites = []string{"https://ya.ru/", "https://vk.com/"}

const (
	every      = time.Minute
	flushEvery = 10 * time.Minute
	// Timeout bounds one request; a site slower than this is no answer.
	Timeout = 4 * time.Second
)

// Fetch asks one site and returns how long the answer took. measured is false
// when no request could be made at all.
type Fetch func(ctx context.Context, url string) (ms int, measured bool, err error)

// Sources is what the prober reads.
type Sources struct {
	// Enabled is tester mode.
	Enabled func() bool
	Fetch   Fetch
	Logf    func(format string, args ...any)
}

// Prober takes one look a minute.
type Prober struct {
	src Sources
	now func() time.Time

	from      int64 // the first minute of the run, unix minutes
	values    []string
	lastFlush time.Time
}

// New makes a prober over src.
func New(src Sources) *Prober { return &Prober{src: src, now: time.Now} }

// Run looks every minute until ctx ends, then writes what it has.
func (p *Prober) Run(ctx context.Context) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			p.flush()
			return
		case <-t.C:
			p.Look(ctx)
		}
	}
}

// Look measures the current minute.
func (p *Prober) Look(ctx context.Context) {
	if !p.src.Enabled() {
		p.flush()
		return
	}
	minute := p.now().Unix() / 60
	p.add(minute, p.measure(ctx))
	if p.now().Sub(p.lastFlush) >= flushEvery {
		p.flush()
	}
}

// measure asks every site at once: the fastest answer, "x" for none, "-" for
// nothing measured.
func (p *Prober) measure(ctx context.Context) string {
	type result struct {
		ms       int
		measured bool
		ok       bool
	}
	results := make([]result, len(Sites))
	var wg sync.WaitGroup
	for i, site := range Sites {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ms, measured, err := p.src.Fetch(ctx, site)
			results[i] = result{ms: ms, measured: measured, ok: measured && err == nil}
		}()
	}
	wg.Wait()
	best, measured := -1, false
	for _, r := range results {
		measured = measured || r.measured
		if r.ok && (best < 0 || r.ms < best) {
			best = r.ms
		}
	}
	switch {
	case best >= 0:
		return strconv.Itoa(max(best, 1))
	case measured:
		return "x"
	default:
		return "-"
	}
}

// add appends a minute to the run; a gap longer than a flush starts a new one.
func (p *Prober) add(minute int64, v string) {
	if len(p.values) > 0 {
		next := p.from + int64(len(p.values))
		if minute < next {
			return
		}
		if minute-next > int64(flushEvery/time.Minute) {
			p.flush()
		} else {
			for ; next < minute; next++ {
				p.values = append(p.values, "-")
			}
		}
	}
	if len(p.values) == 0 {
		p.from = minute
		p.lastFlush = p.now()
	}
	p.values = append(p.values, v)
}

func (p *Prober) flush() {
	if len(p.values) > 0 {
		p.src.Logf("[rureach] minutes from=%d ms=%s", p.from*60, strings.Join(p.values, ","))
	}
	p.values = nil
}
