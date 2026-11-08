// Package testerlog streams the engine log to the hub while the install is in
// tester mode, so a problem a tester hits in the field can be read on the
// admin side as it happens, without asking for a support report.
//
// Lines come from the logger's tap into a bounded buffer and leave once a
// minute, sanitized (logging.SanitizeLog: no IPs, keys, tokens or config
// links). When tester mode switches on, the recent tail of the log file goes
// first, so what led up to the switch is there too. Tester mode off means
// nothing is buffered and nothing is sent.
//
// The same call carries commands back: the hub answers a chunk with what an
// operator asked of this install, a kind from Options.Commands and a nonce. The
// result joins the stream as lines of source «<source> nonce=…», and the batch
// holding the last of them acknowledges the nonce. A minute with nothing to ship
// sends a heartbeat, so the hub knows the install is on the line and a command
// waits a minute at most. Unknown kinds are ignored: a command carries no data
// and runs no code.
package testerlog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"NekkoVPN/core/logging"
)

const (
	// FlushInterval is how often buffered lines are shipped.
	FlushInterval = time.Minute
	// pollInterval is how often the tester switch is read. The switch is not
	// read from the tap: the tap runs under the logger's lock, and the preset
	// manager logs under its own.
	pollInterval = 5 * time.Second
	// backfillLines is how much of the log file goes out when tester mode
	// switches on.
	backfillLines = 2000
	// maxBuffered bounds memory while the hub is unreachable; the oldest
	// lines go first and are counted as dropped.
	maxBuffered = 20000
	// maxBatchBytes keeps one request under the hub's 1 MB body limit with
	// room for the JSON envelope.
	maxBatchBytes = 800 << 10
	// maxLineBytes cuts a single runaway line (a config dump).
	maxLineBytes = 8 << 10
	// commandTimeout bounds one command: its caller stops waiting soon after.
	commandTimeout = 2 * time.Minute
)

// Command is one thing the hub may ask of this install. Run only reads the
// machine and returns its report; Source names the lines in the stream. Params
// is what the operator attached, empty for a kind that takes nothing; Run
// validates it, because the hub's word is not enough for what a machine does.
type Command struct {
	Source string
	Run    func(ctx context.Context, params json.RawMessage) []string
}

// Options wires the shipper to the rest of the core.
type Options struct {
	URL string
	// Enabled reports whether tester mode is on.
	Enabled func() bool
	// Backfill returns the recent tail of the log file, oldest first.
	Backfill func(n int) []string
	// Identity returns the install id, platform and app version.
	Identity func() (install, platform, version string)
	// Commands is the whitelist of what the hub may ask, by kind.
	Commands map[string]Command
	Client   *http.Client
}

// Shipper buffers log lines and ships them while tester mode is on.
type Shipper struct {
	opts Options

	mu      sync.Mutex
	on      bool
	lines   []string
	dropped int
	// acks are the nonces whose result is buffered; they leave with the batch
	// that holds the last line of it.
	acks []string
	// running guards against a command delivered twice.
	running map[string]bool
	// wake flushes as soon as a command's result is buffered.
	wake chan struct{}
}

// New builds a Shipper. It sends nothing until Run is started.
func New(opts Options) *Shipper {
	if opts.Client == nil {
		opts.Client = &http.Client{Timeout: 20 * time.Second}
	}
	return &Shipper{opts: opts, running: map[string]bool{}, wake: make(chan struct{}, 1)}
}

// Tap receives one log line. Install it with logging.Logger.SetTap.
func (s *Shipper) Tap(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.on {
		return
	}
	s.pushLocked(line)
}

func (s *Shipper) pushLocked(line string) {
	if len(line) > maxLineBytes {
		line = line[:maxLineBytes] + " …[обрезано]"
	}
	s.lines = append(s.lines, line)
	if over := len(s.lines) - maxBuffered; over > 0 {
		s.lines = append(s.lines[:0:0], s.lines[over:]...)
		s.dropped += over
	}
}

// Run follows the tester switch and ships the buffer until ctx is done.
func (s *Shipper) Run(ctx context.Context) {
	poll := time.NewTicker(pollInterval)
	defer poll.Stop()
	flush := time.NewTicker(FlushInterval)
	defer flush.Stop()
	s.follow()
	for {
		select {
		case <-ctx.Done():
			return
		case <-poll.C:
			if s.follow() {
				// Ship the backfill at once: the tester switched on because
				// something is wrong right now.
				s.Flush(ctx)
			}
		case <-flush.C:
			if !s.Flush(ctx) {
				s.heartbeat(ctx)
			}
		case <-s.wake:
			s.Flush(ctx)
		}
	}
}

// follow syncs the buffer with the tester switch and reports whether it just
// switched on.
func (s *Shipper) follow() bool {
	on := s.opts.Enabled != nil && s.opts.Enabled()
	s.mu.Lock()
	was := s.on
	s.on = on
	if !on {
		s.lines, s.dropped, s.acks = nil, 0, nil
	}
	s.mu.Unlock()
	if !on || was {
		return false
	}
	var tail []string
	if s.opts.Backfill != nil {
		tail = s.opts.Backfill(backfillLines)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Lines tapped since the switch was read are already in the buffer and
	// also at the end of the file; the backfill goes in front of them.
	live := s.lines
	s.lines = nil
	for _, l := range trimOverlap(tail, live) {
		s.pushLocked(l)
	}
	s.pushLocked(fmt.Sprintf("%s [testerlog] ===== режим тестера включён, выше хвост лога до включения =====",
		time.Now().Format("2006/01/02 15:04:05.000000")))
	for _, l := range live {
		s.pushLocked(l)
	}
	return true
}

// trimOverlap drops the end of tail that repeats the start of live.
func trimOverlap(tail, live []string) []string {
	for k := min(len(tail), len(live)); k > 0; k-- {
		match := true
		for i := 0; i < k; i++ {
			if tail[len(tail)-k+i] != live[i] {
				match = false
				break
			}
		}
		if match {
			return tail[:len(tail)-k]
		}
	}
	return tail
}

type chunk struct {
	InstallID string   `json:"install_id"`
	Platform  string   `json:"platform"`
	Version   string   `json:"version"`
	Lines     []string `json:"lines"`
	Dropped   int      `json:"dropped"`
	Heartbeat bool     `json:"heartbeat,omitempty"`
	Acks      []string `json:"acks,omitempty"`
}

// command is one entry of the hub's answer.
type command struct {
	Kind   string          `json:"kind"`
	Nonce  string          `json:"nonce"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Flush ships everything buffered, batch by batch, and reports whether it tried
// to send anything. A batch the hub does not take goes back to the front of the
// buffer for the next flush.
func (s *Shipper) Flush(ctx context.Context) (sent bool) {
	if s.opts.URL == "" {
		return false
	}
	for ctx.Err() == nil {
		s.mu.Lock()
		if !s.on || (len(s.lines) == 0 && s.dropped == 0 && len(s.acks) == 0) {
			s.mu.Unlock()
			return sent
		}
		n, size := 0, 0
		for n < len(s.lines) && (n == 0 || size+len(s.lines[n]) <= maxBatchBytes) {
			size += len(s.lines[n]) + 1
			n++
		}
		batch := append([]string(nil), s.lines[:n]...)
		dropped := s.dropped
		s.lines = s.lines[n:]
		s.dropped = 0
		// A result is whole only once its last line left: acknowledge with the
		// batch that empties the buffer.
		var acks []string
		if len(s.lines) == 0 {
			acks, s.acks = s.acks, nil
		}
		s.mu.Unlock()

		sent = true
		if err := s.send(ctx, chunk{Lines: batch, Dropped: dropped, Acks: acks}); err != nil {
			s.mu.Lock()
			if s.on {
				s.lines = append(batch, s.lines...)
				s.dropped += dropped
				s.acks = append(acks, s.acks...)
				if over := len(s.lines) - maxBuffered; over > 0 {
					s.lines = s.lines[over:]
					s.dropped += over
				}
			}
			s.mu.Unlock()
			return sent
		}
	}
	return sent
}

// heartbeat says «on the line» when there was nothing to ship, and picks up
// commands from the answer.
func (s *Shipper) heartbeat(ctx context.Context) {
	s.mu.Lock()
	on := s.on
	s.mu.Unlock()
	if !on || s.opts.URL == "" {
		return
	}
	_ = s.send(ctx, chunk{Heartbeat: true})
}

// dispatch starts what the hub asked for. Unknown kinds are ignored.
func (s *Shipper) dispatch(ctx context.Context, commands []command) {
	for _, cmd := range commands {
		c, ok := s.opts.Commands[cmd.Kind]
		if !ok || cmd.Nonce == "" {
			continue
		}
		s.mu.Lock()
		dup := s.running[cmd.Nonce]
		s.running[cmd.Nonce] = true
		s.mu.Unlock()
		if !dup {
			go s.runCommand(ctx, c, cmd.Nonce, cmd.Params)
		}
	}
}

// runCommand buffers one command's report and flushes it without waiting for
// the minute: someone is waiting for it.
func (s *Shipper) runCommand(ctx context.Context, c Command, nonce string, params json.RawMessage) {
	runCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	report := c.Run(runCtx, params)
	stamp := time.Now().Format("2006/01/02 15:04:05.000000")
	s.mu.Lock()
	delete(s.running, nonce)
	if s.on {
		for _, line := range report {
			s.pushLocked(fmt.Sprintf("%s [%s nonce=%s] %s", stamp, c.Source, nonce, line))
		}
		s.pushLocked(fmt.Sprintf("%s [%s nonce=%s] ===== конец =====", stamp, c.Source, nonce))
		s.acks = append(s.acks, nonce)
	}
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Shipper) send(ctx context.Context, c chunk) error {
	batch := c.Lines
	c.Lines = make([]string, len(batch))
	if s.opts.Identity != nil {
		c.InstallID, c.Platform, c.Version = s.opts.Identity()
	}
	if c.InstallID == "" {
		return fmt.Errorf("testerlog: no install id")
	}
	for i, l := range batch {
		c.Lines[i] = logging.SanitizeLog(l)
	}
	body, err := json.Marshal(c)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.opts.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.opts.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("testerlog: hub answered %d", resp.StatusCode)
	}
	// An answer that does not parse costs a command, never the batch: the hub
	// has it.
	var answer struct {
		Commands []command `json:"commands"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&answer); err == nil {
		s.dispatch(ctx, answer.Commands)
	}
	return nil
}
