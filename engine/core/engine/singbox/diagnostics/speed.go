package diag

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	vpn "NekkoVPN/core/engine/singbox/process"
)

// The load round reads 1 MiB, which says a profile works and nothing about how
// fast: at 100 ms a request the download is over before TCP leaves slow start.
// A speed round asks what a tester's home line gets through our path: several
// flows at once, for a fixed time, down and then up, as a speed test does. It
// runs each profile in a core of its own beside the tunnel, as load.go does, so
// it measures the profile's path from the card and not whatever the tunnel
// routes.
//
// It moves hundreds of megabytes a profile, so it runs only on the operator's
// request (the hub's speed_profiles), never on a timer and never on a mobile
// network, and the operator can name one profile.
//
// The target is Cloudflare's speed endpoints. Straight from a Russian network
// Cloudflare is slowed, but here every byte leaves through the profile's exit,
// abroad, where it is not.
const (
	speedStreams = 4
	speedWindow  = 10 * time.Second
	// speedDownBytes and speedUpBytes size one request; a flow that finishes one
	// before the window ends starts the next.
	speedDownBytes = 100 << 20
	speedUpBytes   = 25 << 20
	// speedNowWait bounds a round on request: a core's start, the address check
	// and two windows a profile, with room to spare.
	speedNowWait = 10 * time.Minute
)

// speedPlan is one round's targets and limits; a test points it at its server.
type speedPlan struct {
	DownURL string
	UpURL   string
	Streams int
	Window  time.Duration
}

var defaultSpeedPlan = speedPlan{
	DownURL: fmt.Sprintf("https://speed.cloudflare.com/__down?bytes=%d", speedDownBytes),
	UpURL:   "https://speed.cloudflare.com/__up",
	Streams: speedStreams,
	Window:  speedWindow,
}

// speedProfileTag is what the hub may name: a profile's tag, nothing else.
var speedProfileTag = regexp.MustCompile(`^p-[A-Za-z0-9_-]{1,40}$`)

// speedParams are the hub's: an optional profile tag.
type speedParams struct {
	Profile string `json:"profile"`
}

// SpeedNow answers the hub's speed_profiles. Like LoadNow it returns at once:
// the profiles' lines follow as [diag:speed], closed by «on request: done».
func (r *Runner) SpeedNow(params json.RawMessage) []string {
	return r.speedNow(vpn.CoreBinary(), physicalIfaces(), params, defaultSpeedPlan)
}

func (r *Runner) speedNow(binary string, ifaces []physIface, params json.RawMessage, plan speedPlan) []string {
	var p speedParams
	if len(params) > 0 && string(params) != "null" {
		if err := json.Unmarshal(params, &p); err != nil {
			return []string{`error="params are not readable"`}
		}
	}
	if p.Profile != "" && !speedProfileTag.MatchString(p.Profile) {
		return []string{`error="profile is not a profile tag"`}
	}
	targets, skipped := r.prepareLoad(ifaces)
	if skipped != "" {
		return []string{fmt.Sprintf("error=%q", skipped)}
	}
	if p.Profile != "" {
		var only []loadTarget
		for _, t := range targets {
			if t.Tag == p.Profile {
				only = append(only, t)
			}
		}
		if len(only) == 0 {
			return []string{fmt.Sprintf("error=%q", "no profile "+p.Profile+" in the config")}
		}
		targets = only
	}
	r.mu.Lock()
	// One heavy round at a time: a load round's burst or a voice series beside
	// the flows would read each other's lines wrong.
	if r.loadBusy {
		r.mu.Unlock()
		return []string{`error="a load or speed round is running, ask again when it is done"`}
	}
	if r.voiceBusy {
		r.mu.Unlock()
		return []string{`error="a voice round is running: ask again in a minute"`}
	}
	r.loadBusy = true
	r.mu.Unlock()
	go func() {
		defer func() {
			r.mu.Lock()
			r.loadBusy = false
			r.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), speedNowWait)
		defer cancel()
		r.runSpeed(ctx, binary, targets, ifaces[0], plan)
		r.log.Logf("[diag:speed] on request: done")
	}()
	return []string{fmt.Sprintf("started profiles=%d streams=%d window=%s", len(targets), plan.Streams, plan.Window)}
}

func (r *Runner) runSpeed(ctx context.Context, binary string, targets []loadTarget, iface physIface, plan speedPlan) {
	// The load round learns the network only when it runs; a speed round asks
	// itself, since on a mobile network it must not start at all.
	mobile := onMobile(iface.Name, loadSelfNet(iface.IP, r.stunList()))
	r.mu.Lock()
	r.loadMobile = mobile
	r.mu.Unlock()
	if mobile {
		r.log.Logf("[diag:speed] skipped: mobile network, a speed round costs hundreds of megabytes")
		return
	}
	for _, t := range targets {
		if ctx.Err() != nil {
			return
		}
		r.log.Logf("[diag:speed] %s", speedLine(ctx, binary, t, iface, plan))
	}
}

// speedLine carries the tag, the knobs as the load line has them, and the two
// directions. down and up are the window's bytes over the window, in Mbit/s;
// peak is the best second, which leaves out TCP's ramp at the start.
func speedLine(ctx context.Context, binary string, t loadTarget, iface physIface, plan speedPlan) string {
	head := t.Tag + " proto=" + t.Proto + loadKnobs(t.Outbound)
	if tcp, dead := addrDead(ctx, t, iface.IP); dead {
		return fmt.Sprintf("%s addr_dead=1 tcp=%d/%d reply=0/%d", head, tcp, addrFlows, addrFlows)
	}
	proxy, stop, err := startProfile(ctx, binary, t.Outbound, iface.Name)
	if err != nil {
		return fmt.Sprintf("%s load_error=%q", head, err.Error())
	}
	defer stop()
	client := profileClient(proxy, 0)
	down := speedDown(ctx, client, plan)
	up := speedUp(ctx, client, plan)
	return fmt.Sprintf("%s streams=%d window=%s down=%s up=%s", head, plan.Streams, plan.Window, down, up)
}

// speedResult is one direction of a profile.
type speedResult struct {
	bytes   int64
	window  time.Duration
	perSec  []int64 // bytes in each whole second of the window
	failed  int     // requests that ended in an error before the window did
	lastErr string  // the last of them: timeout, closed or httpNNN
}

// String renders «187.3Mbps(peak=221.0,mb=234,fail=0)»; a direction with no
// byte says why: «0(fail=4,end=closed)».
func (s speedResult) String() string {
	if s.bytes == 0 {
		end := s.lastErr
		if end == "" {
			end = "none"
		}
		return fmt.Sprintf("0(fail=%d,end=%s)", s.failed, end)
	}
	var peak int64
	for _, b := range s.perSec {
		if b > peak {
			peak = b
		}
	}
	return fmt.Sprintf("%.1fMbps(peak=%.1f,mb=%d,fail=%d)",
		mbps(s.bytes, s.window), mbps(peak, time.Second), s.bytes>>20, s.failed)
}

func mbps(bytes int64, d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	return float64(bytes) * 8 / d.Seconds() / 1e6
}

// meter counts the bytes of all flows and samples them once a second.
type meter struct {
	total  atomic.Int64
	mu     sync.Mutex
	perSec []int64
}

func (m *meter) sample(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var last int64
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			now := m.total.Load()
			m.mu.Lock()
			m.perSec = append(m.perSec, now-last)
			m.mu.Unlock()
			last = now
		}
	}
}

// failures keeps the count and the kind of the requests that broke early.
type failures struct {
	mu   sync.Mutex
	n    int
	last string
}

func (f *failures) add(end string) {
	f.mu.Lock()
	f.n++
	f.last = end
	f.mu.Unlock()
}

// speedRun runs plan.Streams flows of one direction until the window closes and
// counts what they moved. A request the window cuts is the measure working,
// not a failure.
func speedRun(ctx context.Context, plan speedPlan, flow func(ctx context.Context, m *meter) string) speedResult {
	window, cancel := context.WithTimeout(ctx, plan.Window)
	defer cancel()
	m := &meter{}
	f := &failures{}
	start := time.Now()
	go m.sample(window)
	var wg sync.WaitGroup
	for i := 0; i < plan.Streams; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for window.Err() == nil {
				end := flow(window, m)
				if window.Err() != nil {
					return
				}
				if end != "ok" {
					f.add(end)
					// A flow that breaks at once must not spin: the path is the
					// thing being measured, and a refused request is its answer.
					select {
					case <-window.Done():
						return
					case <-time.After(200 * time.Millisecond):
					}
				}
			}
		}()
	}
	wg.Wait()
	took := time.Since(start)
	if took > plan.Window {
		took = plan.Window
	}
	m.mu.Lock()
	perSec := append([]int64(nil), m.perSec...)
	m.mu.Unlock()
	f.mu.Lock()
	defer f.mu.Unlock()
	return speedResult{bytes: m.total.Load(), window: took, perSec: perSec, failed: f.n, lastErr: f.last}
}

// countingReader adds what passes through it to the meter.
type countingReader struct {
	r io.Reader
	m *meter
}

func (c countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.m.total.Add(int64(n))
	return n, err
}

func speedDown(ctx context.Context, client *http.Client, plan speedPlan) speedResult {
	return speedRun(ctx, plan, func(ctx context.Context, m *meter) string {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, plan.DownURL, nil)
		if err != nil {
			return "closed"
		}
		resp, err := client.Do(req)
		if err != nil {
			return loadEnd(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Sprintf("http%d", resp.StatusCode)
		}
		if _, err := io.Copy(io.Discard, countingReader{resp.Body, m}); err != nil {
			return loadEnd(err)
		}
		return "ok"
	})
}

// zeros is an endless body of zero bytes; the request's length caps it.
type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func speedUp(ctx context.Context, client *http.Client, plan speedPlan) speedResult {
	return speedRun(ctx, plan, func(ctx context.Context, m *meter) string {
		body := countingReader{io.LimitReader(zeros{}, speedUpBytes), m}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, plan.UpURL, body)
		if err != nil {
			return "closed"
		}
		req.ContentLength = speedUpBytes
		req.Header.Set("Content-Type", "application/octet-stream")
		resp, err := client.Do(req)
		if err != nil {
			return loadEnd(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Sprintf("http%d", resp.StatusCode)
		}
		return "ok"
	})
}
