// Package downtime counts the product's main metric on the client: the minutes
// the tunnel is on and the user's traffic does not go (plans/ROADMAP.md, 3.3).
//
// Every wall-clock minute the tunnel is wanted gets one state. The meter reads
// only what the core already knows about its open connections, so it sends
// nothing over the network, except one request on the direct path when a
// minute went wrong: that request tells our fault from the user's own network
// being down.
//
// A minute is down when an app asked through the tunnel and nothing came back:
// a connection through a profile sent bytes and got no answer for answerWait,
// and no connection through the tunnel received anything in the minute. One
// byte back anywhere makes the minute a flowing one, so the metric counts the
// minutes the tunnel carried nothing, not the minutes it was slow. A tunnel
// nobody uses is idle, not down: without a question there is no answer to miss.
//
// The minutes go to the log every flushEvery and when the tunnel goes off, one
// line per run of minutes: "[downtime] minutes from=<unix> states=oooid-o".
// The hub reads them from testers' logs into its own table. The same run,
// folded into counts, goes to Sources.Report: the telemetry event "tunnel" of
// an ordinary user (docs/TELEMETRY.md), which carries no line of the log.
//
// A minute measures pauses only as fine as its looks, 5 seconds. The core
// times shorter ones itself (sing-box-lx hikkari 0015): a stall runs from the
// first byte an app sent through the tunnel after the last byte back to the
// next byte back, on any connection. Next to the minutes goes one more line
// with each minute's stalls past each threshold, their number and their whole
// length in ms: "[downtime] stalls from=<unix> bounds=300,1000,3000
// v=.;2:950,1:1200,0:0;-". "." is a minute without a stall, "-" one not
// measured (an older core, or the minute unseen).
//
// For a tester the log also says how much went through the tunnel and on what
// (owner, 18.11: «тестеры добровольно дали разрешение на все»). Each minute's
// bytes, from the same looks at the connections: "[downtime] bytes from=<unix>
// v=1200,56000;.;-", per minute the bytes up and down through the tunnel, "." a
// minute that carried nothing, "-" one not measured. Traffic past the tunnel is
// the tester's own business, not our service (owner, 18.11), and is not counted
// at all: the meter skips direct connections. Clients 1.0.93 wrote two more
// figures, up and down past the tunnel; the hub drops them. A connection that opened and closed between two looks, or the
// last bytes of one that closed, are not seen: the figure is a little short,
// never long. And what the tunnel runs on, when it changes and with every run:
// "[downtime] state at=<unix> mode=proxy proto=hikkari profile="амстердам-006"".
// Neither goes into Period: an ordinary user's telemetry carries no traffic.
package downtime

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// State is what one minute of a wanted tunnel was.
type State byte

const (
	// Flowing: something came back through the tunnel.
	Flowing State = 'o'
	// Idle: nothing asked to go through the tunnel.
	Idle State = 'i'
	// Down: asked through the tunnel, nothing came back, the direct path answers.
	Down State = 'd'
	// CoreDown: the tunnel was wanted and its core was not up for most of the
	// minute, the direct path answers.
	CoreDown State = 'c'
	// NetDown: nothing came back and the direct path does not answer either:
	// the user's own network, not the tunnel.
	NetDown State = 'n'
	// Unseen: inside a run, a minute the meter did not look at enough (the
	// machine asleep, the app stalled). It says nothing either way.
	Unseen State = '-'
)

// Lost reports whether the minute counts towards the metric.
func (s State) Lost() bool { return s == Down || s == CoreDown }

const (
	// pollEvery: the connection list is answered from the core's memory.
	pollEvery = 5 * time.Second
	// minPolls is how many looks make a minute: half of it.
	minPolls = 6
	// answerWait is how long a connection may wait for an answer to what it
	// sent before it counts as asked and unanswered. A server answers a TLS
	// hello at once; a request to a working site does too.
	answerWait = 5 * time.Second
	// minAsk is the least a connection must have sent to have asked at all, as
	// in autotunnel: a TCP keepalive or a lone ACK is not a question.
	minAsk = 200
	// flushEvery is how often the minutes go to the log while the tunnel stays on.
	flushEvery = 10 * time.Minute
	// stallGap: a pause between looks longer than this means the meter was not
	// running (sleep), so the minutes in between are unseen.
	stallGap = 3 * pollEvery
)

// Conn is one open connection as the core reports it.
type Conn struct {
	ID     string
	Up     uint64
	Down   uint64
	Direct bool
	// Start is when the connection opened. A connection the meter sees for the
	// first time counts with all its bytes only if it opened since the last
	// look; an older one is only a baseline.
	Start time.Time
}

// Sources is what the meter reads.
type Sources struct {
	// Tunnel: wanted is the user's switch (and the service's run marker),
	// running is the core answering.
	Tunnel func() (wanted, running bool)
	// Conns lists the core's open connections. Called only while it runs.
	Conns func() ([]Conn, error)
	// Internet reports whether the direct path reaches ordinary sites. checked
	// is false when it could not be measured.
	Internet func(ctx context.Context) (ok, checked bool)
	// Stalls reads the core's stall totals since it started; ok is false when
	// the core does not time stalls. Nil when the engine cannot.
	Stalls func() (totals StallTotals, ok bool)
	// State is what the tunnel runs on now: the routing mode (proxy, bypass,
	// off), the active profile's protocol and its name. Nil when unknown.
	State func() (mode, proto, profile string)
	// Logf writes the minutes.
	Logf func(format string, args ...any)
	// Report takes each run as counts, when the run is written. Nil when
	// nobody wants them.
	Report func(Period)
}

// Period is one run of minutes folded into counts: what an ordinary user's
// telemetry carries instead of the log lines. It says how many minutes were
// what and how long the tunnel stalled, and nothing about where traffic went.
type Period struct {
	// From is the run's first minute, unix seconds.
	From int64
	// Minutes counts the run's minutes by state; unseen ones are not counted.
	Minutes map[State]int
	// StallMinutes is how many of the minutes had their stalls measured.
	StallMinutes int
	// StallBoundsMS are the core's thresholds; StallCount and StallMS are the
	// stalls of the measured minutes that lasted at least each of them and
	// their whole length. All three are empty when StallMinutes is zero.
	StallBoundsMS []int64
	StallCount    []int64
	StallMS       []int64
}

// StallTotals are the core's stalls since it started, one value per bound:
// how many lasted at least the bound, and their whole length in ms.
type StallTotals struct {
	BoundsMS []int64
	Count    []int64
	MS       []int64
}

// minuteBytes are one minute's bytes through the tunnel; nil when the minute
// was not measured.
type minuteBytes struct {
	tunUp, tunDown uint64
}

func (b *minuteBytes) empty() bool {
	return b.tunUp == 0 && b.tunDown == 0
}

// minuteStalls are one minute's stalls; nil when they were not measured.
type minuteStalls struct {
	count, ms []int64
}

// Meter turns looks at the tunnel into minutes.
type Meter struct {
	src Sources
	now func() time.Time

	conns map[string]*connState

	// The minute being looked at.
	minute     int64
	polls      int
	coreDown   int
	got        bool
	unanswered bool
	minBytes   minuteBytes
	lastPoll   time.Time
	// prevPoll is the look before this one while the tunnel stays on.
	prevPoll time.Time

	// Finished minutes not yet written.
	from      int64
	states    []State
	stalls    []*minuteStalls
	bytes     []*minuteBytes
	lastFlush time.Time

	// lastState is the state line last written, "" while the tunnel is off.
	lastState string

	// The core's stall totals at the last minute closed, and their bounds.
	stallBase   *StallTotals
	stallBounds []int64
}

type connState struct {
	up, down uint64
	// pendingSince: when this connection sent something still unanswered.
	pendingSince time.Time
	pendingBytes uint64
}

// New makes a meter over src.
func New(src Sources) *Meter {
	return &Meter{src: src, now: time.Now, conns: map[string]*connState{}}
}

// Run looks every pollEvery until ctx ends, then writes what it has.
func (m *Meter) Run(ctx context.Context) {
	t := time.NewTicker(pollEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			m.closeMinute(context.Background())
			m.flush()
			return
		case <-t.C:
			m.Poll(ctx)
		}
	}
}

// Poll takes one look.
func (m *Meter) Poll(ctx context.Context) {
	now := m.now()
	wanted, running := m.src.Tunnel()
	minute := now.Unix() / 60
	stalled := !m.lastPoll.IsZero() && now.Sub(m.lastPoll) > stallGap
	if m.polls > 0 && (minute != m.minute || stalled) {
		m.closeMinute(ctx)
	}
	if !wanted {
		m.lastPoll, m.prevPoll = time.Time{}, time.Time{}
		m.conns = map[string]*connState{}
		m.stallBase = nil
		m.flush()
		m.lastState = ""
		return
	}
	if m.polls == 0 {
		m.minute = minute
	}
	m.prevPoll = m.lastPoll
	if stalled {
		m.prevPoll = time.Time{}
	}
	m.lastPoll = now
	m.polls++
	if !running {
		m.coreDown++
		m.conns = map[string]*connState{}
		m.stallBase = nil // a new core counts from zero
	} else if conns, err := m.src.Conns(); err == nil {
		m.look(now, conns)
	}
	if !m.lastFlush.IsZero() && now.Sub(m.lastFlush) >= flushEvery {
		m.flush()
	}
}

// look reads the connection list into the minute.
func (m *Meter) look(now time.Time, conns []Conn) {
	open := make(map[string]*connState, len(conns))
	for _, c := range conns {
		if c.Direct {
			continue
		}
		s := m.conns[c.ID]
		if s == nil {
			s = &connState{}
			if m.prevPoll.IsZero() || c.Start.Before(m.prevPoll) {
				s.up, s.down = c.Up, c.Down
			}
		}
		open[c.ID] = s
		var up, down uint64
		if c.Up > s.up {
			up = c.Up - s.up
		}
		if c.Down > s.down {
			down = c.Down - s.down
		}
		m.minBytes.tunUp += up
		m.minBytes.tunDown += down
		if c.Down > s.down {
			m.got = true
			s.pendingSince, s.pendingBytes = time.Time{}, 0
		} else if c.Up > s.up {
			// Sent since the last look: it has waited no longer than that.
			if s.pendingSince.IsZero() {
				s.pendingSince = now
			}
			s.pendingBytes += c.Up - s.up
		}
		s.up, s.down = c.Up, c.Down
		if !s.pendingSince.IsZero() && s.pendingBytes >= minAsk && now.Sub(s.pendingSince) >= answerWait {
			m.unanswered = true
		}
	}
	m.conns = open
}

// closeMinute decides the minute looked at and starts on the next.
func (m *Meter) closeMinute(ctx context.Context) {
	if m.polls == 0 {
		return
	}
	stalls := m.readStalls()
	if m.polls >= minPolls {
		bytes := m.minBytes
		m.add(m.minute, m.verdict(ctx), stalls, &bytes)
	}
	m.polls, m.coreDown, m.got, m.unanswered = 0, 0, false, false
	m.minBytes = minuteBytes{}
	m.writeState(false)
}

// writeState logs what the tunnel runs on when it changed, or always.
func (m *Meter) writeState(always bool) {
	if m.src.State == nil {
		return
	}
	if wanted, running := m.src.Tunnel(); !wanted || !running {
		return
	}
	mode, proto, profile := m.src.State()
	line := fmt.Sprintf("mode=%s proto=%s profile=%q", orDash(mode), orDash(proto), profile)
	if line == m.lastState && !always {
		return
	}
	m.lastState = line
	m.src.Logf("[downtime] state at=%d %s", m.now().Unix(), line)
}

func orDash(s string) string {
	if s == "" || strings.ContainsAny(s, " \t\"") {
		return "-"
	}
	return s
}

func (m *Meter) verdict(ctx context.Context) State {
	var s State
	switch {
	case m.got:
		return Flowing
	case m.coreDown*2 >= m.polls:
		s = CoreDown
	case m.unanswered:
		s = Down
	default:
		return Idle
	}
	if ok, checked := m.src.Internet(ctx); checked && !ok {
		return NetDown
	}
	return s
}

// readStalls takes the minute's stalls from the core's totals: the change
// since the last minute closed. The first minute of a core has no base and is
// not measured; totals that went down mean a new core, and start over.
func (m *Meter) readStalls() *minuteStalls {
	if m.src.Stalls == nil {
		return nil
	}
	// The tunnel may have just gone off: a core still up still has the minute.
	if _, running := m.src.Tunnel(); !running {
		m.stallBase = nil
		return nil
	}
	totals, ok := m.src.Stalls()
	if !ok || len(totals.Count) != len(totals.BoundsMS) || len(totals.MS) != len(totals.BoundsMS) {
		m.stallBase = nil
		return nil
	}
	base := m.stallBase
	m.stallBase = &totals
	if base == nil || !sameBounds(base.BoundsMS, totals.BoundsMS) {
		return nil
	}
	out := &minuteStalls{count: make([]int64, len(totals.Count)), ms: make([]int64, len(totals.MS))}
	for i := range totals.Count {
		out.count[i] = totals.Count[i] - base.Count[i]
		out.ms[i] = totals.MS[i] - base.MS[i]
		if out.count[i] < 0 || out.ms[i] < 0 {
			return nil
		}
	}
	return out
}

func sameBounds(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// add appends a minute to the run, marking any skipped minutes unseen. A gap
// longer than a flush starts a new run instead of a line of dashes.
func (m *Meter) add(minute int64, s State, stalls *minuteStalls, bytes *minuteBytes) {
	if len(m.states) > 0 && minute-(m.from+int64(len(m.states))) > int64(flushEvery/time.Minute) {
		m.flush()
	}
	if len(m.states) == 0 {
		m.from = minute
		if m.lastFlush.IsZero() {
			m.lastFlush = m.now()
		}
	}
	next := m.from + int64(len(m.states))
	if minute < next {
		return // the clock went back; that minute is written already
	}
	if stalls != nil && !sameBounds(m.stallBounds, m.stallBase.BoundsMS) {
		if m.hasStalls() {
			m.flush()
			m.from = minute
			m.lastFlush = m.now()
			next = minute
		}
		m.stallBounds = m.stallBase.BoundsMS
	}
	for ; next < minute; next++ {
		m.states = append(m.states, Unseen)
		m.stalls = append(m.stalls, nil)
		m.bytes = append(m.bytes, nil)
	}
	m.states = append(m.states, s)
	m.stalls = append(m.stalls, stalls)
	m.bytes = append(m.bytes, bytes)
}

func (m *Meter) hasStalls() bool {
	for _, s := range m.stalls {
		if s != nil {
			return true
		}
	}
	return false
}

// flush writes the run and starts a new one.
func (m *Meter) flush() {
	if len(m.states) > 0 {
		var b strings.Builder
		for _, s := range m.states {
			b.WriteByte(byte(s))
		}
		m.src.Logf("[downtime] minutes from=%d states=%s", m.from*60, b.String())
		if m.hasStalls() {
			m.src.Logf("[downtime] stalls from=%d bounds=%s v=%s", m.from*60, joinInts(m.stallBounds, ","), m.stallLine())
		}
		if line, ok := m.bytesLine(); ok {
			m.src.Logf("[downtime] bytes from=%d v=%s", m.from*60, line)
		}
		if m.src.Report != nil {
			m.src.Report(m.period())
		}
		if m.lastState != "" {
			m.writeState(true)
		}
	}
	m.states = nil
	m.stalls = nil
	m.bytes = nil
	m.lastFlush = time.Time{}
}

// period folds the run into counts.
func (m *Meter) period() Period {
	p := Period{From: m.from * 60, Minutes: map[State]int{}}
	for _, s := range m.states {
		if s != Unseen {
			p.Minutes[s]++
		}
	}
	for _, s := range m.stalls {
		if s == nil || len(s.count) != len(m.stallBounds) {
			continue
		}
		if p.StallMinutes == 0 {
			p.StallBoundsMS = append([]int64(nil), m.stallBounds...)
			p.StallCount = make([]int64, len(m.stallBounds))
			p.StallMS = make([]int64, len(m.stallBounds))
		}
		p.StallMinutes++
		for j := range s.count {
			p.StallCount[j] += s.count[j]
			p.StallMS[j] += s.ms[j]
		}
	}
	return p
}

// bytesLine spells the run's bytes: per minute "." when nothing went, "-" not
// measured, else "tunnel up,tunnel down"; minutes by
// ";". ok is false when no minute carried anything: such a run says nothing
// the minutes line does not.
func (m *Meter) bytesLine() (string, bool) {
	var b strings.Builder
	carried := false
	for i, v := range m.bytes {
		if i > 0 {
			b.WriteByte(';')
		}
		switch {
		case v == nil:
			b.WriteByte('-')
		case v.empty():
			b.WriteByte('.')
		default:
			carried = true
			fmt.Fprintf(&b, "%d,%d", v.tunUp, v.tunDown)
		}
	}
	return b.String(), carried
}

// stallLine spells the run's stalls: per minute "." without a stall, "-" not
// measured, else "count:ms" per bound joined by commas; minutes by ";".
func (m *Meter) stallLine() string {
	var b strings.Builder
	for i, s := range m.stalls {
		if i > 0 {
			b.WriteByte(';')
		}
		switch {
		case s == nil:
			b.WriteByte('-')
		case !anyStall(s):
			b.WriteByte('.')
		default:
			for j := range s.count {
				if j > 0 {
					b.WriteByte(',')
				}
				b.WriteString(strconv.FormatInt(s.count[j], 10))
				b.WriteByte(':')
				b.WriteString(strconv.FormatInt(s.ms[j], 10))
			}
		}
	}
	return b.String()
}

func anyStall(s *minuteStalls) bool {
	for _, c := range s.count {
		if c != 0 {
			return true
		}
	}
	return false
}

func joinInts(v []int64, sep string) string {
	parts := make([]string, len(v))
	for i, x := range v {
		parts[i] = strconv.FormatInt(x, 10)
	}
	return strings.Join(parts, sep)
}
