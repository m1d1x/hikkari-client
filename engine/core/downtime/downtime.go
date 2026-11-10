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
// The hub reads them from testers' logs into its own table.
package downtime

import (
	"context"
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
	// Logf writes the minutes.
	Logf func(format string, args ...any)
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
	lastPoll   time.Time
	// prevPoll is the look before this one while the tunnel stays on.
	prevPoll time.Time

	// Finished minutes not yet written.
	from      int64
	states    []State
	lastFlush time.Time
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
		m.flush()
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
	if m.polls >= minPolls {
		m.add(m.minute, m.verdict(ctx))
	}
	m.polls, m.coreDown, m.got, m.unanswered = 0, 0, false, false
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

// add appends a minute to the run, marking any skipped minutes unseen. A gap
// longer than a flush starts a new run instead of a line of dashes.
func (m *Meter) add(minute int64, s State) {
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
	for ; next < minute; next++ {
		m.states = append(m.states, Unseen)
	}
	m.states = append(m.states, s)
}

// flush writes the run and starts a new one.
func (m *Meter) flush() {
	if len(m.states) > 0 {
		var b strings.Builder
		for _, s := range m.states {
			b.WriteByte(byte(s))
		}
		m.src.Logf("[downtime] minutes from=%d states=%s", m.from*60, b.String())
	}
	m.states = nil
	m.lastFlush = time.Time{}
}
