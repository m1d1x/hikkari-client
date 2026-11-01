// Package testerlog streams the engine log to the hub while the install is in
// tester mode, so a problem a tester hits in the field can be read on the
// admin side as it happens, without asking for a support report.
//
// Lines come from the logger's tap into a bounded buffer and leave once a
// minute, sanitized (logging.SanitizeLog: no IPs, keys, tokens or config
// links). When tester mode switches on, the recent tail of the log file goes
// first, so what led up to the switch is there too. Tester mode off means
// nothing is buffered and nothing is sent.
package testerlog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
)

// Options wires the shipper to the rest of the core.
type Options struct {
	URL string
	// Enabled reports whether tester mode is on.
	Enabled func() bool
	// Backfill returns the recent tail of the log file, oldest first.
	Backfill func(n int) []string
	// Identity returns the install id, platform and app version.
	Identity func() (install, platform, version string)
	Client   *http.Client
}

// Shipper buffers log lines and ships them while tester mode is on.
type Shipper struct {
	opts Options

	mu      sync.Mutex
	on      bool
	lines   []string
	dropped int
}

// New builds a Shipper. It sends nothing until Run is started.
func New(opts Options) *Shipper {
	if opts.Client == nil {
		opts.Client = &http.Client{Timeout: 20 * time.Second}
	}
	return &Shipper{opts: opts}
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
		s.lines, s.dropped = nil, 0
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
	s.pushLocked(fmt.Sprintf("%s [testerlog] ===== режим тестера включён, выше — хвост лога до включения =====",
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
}

// Flush ships everything buffered, batch by batch. A batch the hub does not
// take goes back to the front of the buffer for the next flush.
func (s *Shipper) Flush(ctx context.Context) {
	if s.opts.URL == "" {
		return
	}
	for ctx.Err() == nil {
		s.mu.Lock()
		if !s.on || (len(s.lines) == 0 && s.dropped == 0) {
			s.mu.Unlock()
			return
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
		s.mu.Unlock()

		if err := s.send(ctx, batch, dropped); err != nil {
			s.mu.Lock()
			if s.on {
				s.lines = append(batch, s.lines...)
				s.dropped += dropped
				if over := len(s.lines) - maxBuffered; over > 0 {
					s.lines = s.lines[over:]
					s.dropped += over
				}
			}
			s.mu.Unlock()
			return
		}
	}
}

func (s *Shipper) send(ctx context.Context, batch []string, dropped int) error {
	c := chunk{Lines: make([]string, len(batch)), Dropped: dropped}
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
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("testerlog: hub answered %d", resp.StatusCode)
	}
	return nil
}
