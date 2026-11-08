package diag

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"time"
)

// A call carries its voice as small UDP datagrams, one every 20 ms. A profile
// that carries UDP inside a TCP stream (VLESS, Naive) holds every datagram
// behind a lost segment until TCP repairs it: the call hears a pause, then a
// burst. A profile that carries datagrams as datagrams (Hysteria2, AmneziaWG)
// loses the one packet, and the codec hides it. Whether that difference is
// audible on testers' networks decides whether Hikkari needs a UDP leg beside
// its TCP profiles (plans/research/2026-11-08-udp-abroad-from-testers.md). The
// round below sends what a call sends and writes what a call would hear.
//
// The series goes through the tunnel, whatever profile is selected. On Windows
// a second series leaves from the physical card past the tunnel: what the
// tester's own network loses before any tunnel. Elsewhere a socket bound to the
// card still goes through the TUN (reach.go), so that control is Windows only.
const (
	voiceEvery = 30 * time.Minute
	// A series costs ~0.3 MB; on a phone's data plan the round runs rarely.
	voiceEveryMobile = 2 * time.Hour
	voiceInterval    = 20 * time.Millisecond
	voicePackets     = 1500 // 30 s at voiceInterval
	voiceSize        = 160  // bytes above UDP: an Opus frame with its RTP header
	// A reply later than this is lost to a call: no jitter buffer waits a second.
	voiceGrace     = time.Second
	voiceGapShort  = 60 * time.Millisecond  // three frames: an audible glitch
	voiceGapLong   = 150 * time.Millisecond // a dropout
	voiceSoftware  = 0x8022                 // STUN SOFTWARE, comprehension-optional
	voiceTargetTag = "cloudflare"
)

// voiceTarget answers each datagram: a STUN binding request comes back with the
// same transaction ID. Cloudflare's anycast answers near whichever end the
// datagram leaves from, so through the tunnel the series times the tunnel and
// not a far server, and from the card it times the tester's own network.
const voiceTarget = "stun.cloudflare.com:3478"

// maybeVoice starts the round when it is allowed and due. Like the load round
// it runs beside the session's loop, and never at the same time as a load
// round, whose bursts would read as the tunnel losing voice.
func (r *Runner) maybeVoice(ctx context.Context) {
	r.mu.Lock()
	allowed := r.reachAllowed
	r.mu.Unlock()
	if allowed == nil || !allowed() {
		return
	}
	r.mu.Lock()
	every := voiceEvery
	if r.voiceMobile {
		every = voiceEveryMobile
	}
	if r.voiceBusy || r.loadBusy || time.Since(r.voiceAt) < every {
		r.mu.Unlock()
		return
	}
	r.voiceAt = time.Now()
	r.voiceBusy = true
	ifaces := r.ifaces
	r.mu.Unlock()
	go func() {
		defer func() {
			r.mu.Lock()
			r.voiceBusy = false
			r.mu.Unlock()
		}()
		for _, line := range r.voiceLines(ctx, ifaces) {
			r.log.Logf("[diag:voice] %s", line)
		}
	}()
}

// VoiceNow answers the hub's probe_voice: a round now, its lines as the answer.
// It takes about a minute, inside a command's wait. The next periodic round
// counts from now.
func (r *Runner) VoiceNow() []string {
	r.mu.Lock()
	switch {
	case r.voiceBusy:
		r.mu.Unlock()
		return []string{`error="a voice round is running, its lines follow as [diag:voice]"`}
	case r.loadBusy:
		r.mu.Unlock()
		return []string{`error="a load round is running: its bursts would read as voice loss"`}
	}
	r.voiceAt = time.Now()
	r.voiceBusy = true
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.voiceBusy = false
		r.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), voiceNowWait)
	defer cancel()
	return r.voiceLines(ctx, physicalIfaces())
}

// voiceNowWait bounds a round on request: two series of ~31 s and the lookups.
const voiceNowWait = 2 * time.Minute

// voiceLines measures one series through the tunnel and, on Windows, one from
// the physical card, a line each.
func (r *Runner) voiceLines(ctx context.Context, ifaces []physIface) []string {
	r.mu.Lock()
	addr, isp := r.voiceAddr, r.baseNet
	r.mu.Unlock()
	ispAt := "baseline" // the network the tunnel-down snapshot saw
	var card *physIface
	if loadOS == "windows" && len(ifaces) > 0 {
		card = &ifaces[0]
		isp, ispAt = netOf(publicIP(card.IP, r.stunList())), "card"
	}
	if isp == "" {
		isp = "unknown"
	}
	cardName := ""
	if card != nil {
		cardName = card.Name
	}
	mobile := onMobile(cardName, isp)
	r.mu.Lock()
	r.voiceMobile = mobile
	r.mu.Unlock()
	network := fmt.Sprintf("isp=%s isp_at=%s mobile=%d", isp, ispAt, boolInt(mobile))

	tag, proto := r.voiceProfile()
	if addr == "" {
		return []string{fmt.Sprintf("path=tun profile=%s proto=%s %s error=%q", tag, proto, network, "target not resolved")}
	}
	lines := []string{fmt.Sprintf("path=tun profile=%s proto=%s %s target=%s %s",
		tag, proto, network, voiceTargetTag, voiceLine(ctx, nil, addr))}
	if card != nil && ctx.Err() == nil {
		lines = append(lines, fmt.Sprintf("path=direct iface=%s %s target=%s %s",
			strings.ReplaceAll(card.Name, " ", "_"), network, voiceTargetTag, voiceLine(ctx, card.IP, addr)))
	}
	return lines
}

// voiceProfile names the profile the tunnel runs on now: the selector's choice,
// followed through groups (an auto group's own choice), with its protocol.
func (r *Runner) voiceProfile() (tag, proto string) {
	var px struct {
		Proxies map[string]struct {
			Now string `json:"now"`
		} `json:"proxies"`
	}
	if err := clashGet("/proxies", &px); err != nil {
		return "unknown", "unknown"
	}
	tag = "proxy"
	for i := 0; i < 4; i++ {
		p, ok := px.Proxies[tag]
		if !ok || p.Now == "" {
			break
		}
		tag = p.Now
	}
	proto = "unknown"
	if config, err := os.ReadFile(r.configPath); err == nil {
		if targets, err := reachTargets(config); err == nil {
			for _, t := range targets {
				if t.Tag == tag {
					proto = t.Proto
				}
			}
		}
	}
	return tag, proto
}

// voiceLine runs one series from local (nil: the default route, the tunnel)
// and formats what it measured.
func voiceLine(ctx context.Context, local net.IP, addr string) string {
	raddr, err := net.ResolveUDPAddr("udp4", addr)
	if err != nil {
		return fmt.Sprintf("error=%q", "resolve: "+err.Error())
	}
	var laddr *net.UDPAddr
	if local != nil {
		laddr = &net.UDPAddr{IP: local}
	}
	conn, err := net.DialUDP("udp4", laddr, raddr)
	if err != nil {
		return fmt.Sprintf("error=%q", "dial: "+err.Error())
	}
	defer conn.Close()
	sent, got, dup, err := voiceSeries(ctx, conn, voicePackets, voiceInterval, voiceGrace)
	if err != nil && len(sent) == 0 {
		return fmt.Sprintf("error=%q", err.Error())
	}
	out := summarizeVoice(sent, got, dup).String()
	if err != nil {
		out += fmt.Sprintf(" error=%q", err.Error())
	}
	return out
}

// voiceRequest is a STUN binding request of voiceSize bytes whose transaction
// ID carries the series and the sequence number.
func voiceRequest(series [8]byte, seq uint32) []byte {
	pad := voiceSize - 20 - 4
	b := make([]byte, voiceSize)
	binary.BigEndian.PutUint16(b[0:], 0x0001)
	binary.BigEndian.PutUint16(b[2:], uint16(4+pad))
	binary.BigEndian.PutUint32(b[4:], 0x2112A442)
	binary.BigEndian.PutUint32(b[8:], seq)
	copy(b[12:20], series[:])
	binary.BigEndian.PutUint16(b[20:], voiceSoftware)
	binary.BigEndian.PutUint16(b[22:], uint16(pad))
	copy(b[24:], "hikkari voice probe")
	for i := 24 + len("hikkari voice probe"); i < len(b); i++ {
		b[i] = ' '
	}
	return b
}

// voiceSeries sends n requests, one every interval on a fixed schedule, and
// collects the replies until grace after the last send. sent[i] is when request
// i left (the slice stops where sending stopped); got[i] is when its first reply
// came, zero if none did; dup counts replies that came again.
func voiceSeries(ctx context.Context, conn *net.UDPConn, n int, interval, grace time.Duration) (sent, got []time.Time, dup int, err error) {
	var series [8]byte
	_, _ = rand.Read(series[:])
	got = make([]time.Time, n)
	start := time.Now()
	_ = conn.SetReadDeadline(start.Add(time.Duration(n)*interval + grace))

	done := make(chan int)
	go func() {
		dups := 0
		buf := make([]byte, 1500)
		for {
			k, rerr := conn.Read(buf)
			if rerr != nil {
				done <- dups
				return
			}
			at := time.Now()
			if k < 20 || string(buf[12:20]) != string(series[:]) {
				continue
			}
			seq := binary.BigEndian.Uint32(buf[8:12])
			if int(seq) >= n {
				continue
			}
			if got[seq].IsZero() {
				got[seq] = at
			} else {
				dups++
			}
		}
	}()

	sent = make([]time.Time, 0, n)
	for i := 0; i < n; i++ {
		if wait := time.Until(start.Add(time.Duration(i) * interval)); wait > 0 {
			select {
			case <-ctx.Done():
				err = ctx.Err()
			case <-time.After(wait):
			}
		}
		if err != nil {
			break
		}
		t := time.Now()
		if _, werr := conn.Write(voiceRequest(series, uint32(i))); werr != nil {
			err = fmt.Errorf("write: %w", werr)
			break
		}
		sent = append(sent, t)
	}
	if err != nil {
		// Stop waiting for replies the series will not use.
		_ = conn.SetReadDeadline(time.Now())
	} else if len(sent) > 0 {
		_ = conn.SetReadDeadline(sent[len(sent)-1].Add(grace))
	}
	dup = <-done
	return sent, got[:len(sent)], dup, err
}

// voiceStats is what a call would hear from one series.
type voiceStats struct {
	Sent, Recv int
	// LossRun is the longest run of consecutive requests with no reply.
	LossRun int
	// RTT percentiles over the replies.
	P50, P95, Max time.Duration
	// Jitter is RFC 3550's running estimate over consecutive replies' RTTs.
	Jitter time.Duration
	// Gaps count silences between consecutive arrivals: a lost run and a stream
	// that stalls, then bursts, both make one.
	Gaps60, Gaps150 int
	GapMax          time.Duration
	// Reorder counts replies that came after a later request's reply.
	Reorder, Dup int
}

func summarizeVoice(sent, got []time.Time, dup int) voiceStats {
	s := voiceStats{Sent: len(sent), Dup: dup}
	type arrival struct {
		seq int
		at  time.Time
	}
	var arrivals []arrival
	var rtts []time.Duration
	run := 0
	var prevRTT time.Duration
	havePrev := false
	var jitter float64
	for i := range sent {
		if got[i].IsZero() {
			run++
			if run > s.LossRun {
				s.LossRun = run
			}
			continue
		}
		run = 0
		rtt := got[i].Sub(sent[i])
		rtts = append(rtts, rtt)
		arrivals = append(arrivals, arrival{i, got[i]})
		if havePrev {
			d := float64(rtt - prevRTT)
			if d < 0 {
				d = -d
			}
			jitter += (d - jitter) / 16
		}
		prevRTT, havePrev = rtt, true
	}
	s.Recv = len(rtts)
	s.Jitter = time.Duration(jitter)
	if s.Recv == 0 {
		return s
	}
	sort.Slice(rtts, func(i, j int) bool { return rtts[i] < rtts[j] })
	s.P50 = rtts[(len(rtts)-1)/2]
	s.P95 = rtts[(len(rtts)*95+99)/100-1]
	s.Max = rtts[len(rtts)-1]

	sort.SliceStable(arrivals, func(i, j int) bool { return arrivals[i].at.Before(arrivals[j].at) })
	maxSeq := -1
	for i, a := range arrivals {
		if a.seq < maxSeq {
			s.Reorder++
		} else {
			maxSeq = a.seq
		}
		if i == 0 {
			continue
		}
		gap := a.at.Sub(arrivals[i-1].at)
		if gap > s.GapMax {
			s.GapMax = gap
		}
		if gap > voiceGapShort {
			s.Gaps60++
		}
		if gap > voiceGapLong {
			s.Gaps150++
		}
	}
	return s
}

// String is the line's measurement part: every count, times in whole ms.
func (s voiceStats) String() string {
	loss := 0.0
	if s.Sent > 0 {
		loss = float64(s.Sent-s.Recv) / float64(s.Sent) * 100
	}
	out := fmt.Sprintf("sent=%d recv=%d loss=%.2f loss_run=%d", s.Sent, s.Recv, loss, s.LossRun)
	if s.Recv == 0 {
		return out
	}
	ms := func(d time.Duration) int64 { return d.Round(time.Millisecond).Milliseconds() }
	return out + fmt.Sprintf(" rtt_p50=%d rtt_p95=%d rtt_max=%d jitter=%.1f gaps60=%d gaps150=%d gap_max=%d reorder=%d dup=%d",
		ms(s.P50), ms(s.P95), ms(s.Max), float64(s.Jitter)/float64(time.Millisecond), s.Gaps60, s.Gaps150, ms(s.GapMax), s.Reorder, s.Dup)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
