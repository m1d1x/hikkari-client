package diag

import (
	"context"
	"encoding/binary"
	"net"
	"strings"
	"testing"
	"time"
)

// voiceFixture builds a series of n requests sent every 20 ms from t0, and replies with
// the given per-request RTTs (0 means lost).
func voiceFixture(rtts []time.Duration) (sent, got []time.Time) {
	t0 := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	for i, rtt := range rtts {
		s := t0.Add(time.Duration(i) * voiceInterval)
		sent = append(sent, s)
		if rtt == 0 {
			got = append(got, time.Time{})
		} else {
			got = append(got, s.Add(rtt))
		}
	}
	return sent, got
}

func voiceSummaryOf(rtts []time.Duration) voiceStats {
	sent, got := voiceFixture(rtts)
	return summarizeVoice(sent, got, 0)
}

func TestVoiceSummaryOfACleanSeries(t *testing.T) {
	rtts := make([]time.Duration, 50)
	for i := range rtts {
		rtts[i] = 40 * time.Millisecond
	}
	s := voiceSummaryOf(rtts)
	if s.Sent != 50 || s.Recv != 50 || s.LossRun != 0 || s.Gaps60 != 0 || s.Reorder != 0 {
		t.Fatalf("clean series: %+v", s)
	}
	if s.P50 != 40*time.Millisecond || s.Max != 40*time.Millisecond || s.Jitter != 0 {
		t.Fatalf("clean series times: %+v", s)
	}
	if s.GapMax != voiceInterval {
		t.Fatalf("gap between arrivals %v, want the send interval", s.GapMax)
	}
}

// A lost run of four leaves a 100 ms silence: one gap over 60 ms, none over 150.
func TestVoiceSummaryCountsALostRunAsAGap(t *testing.T) {
	rtts := make([]time.Duration, 20)
	for i := range rtts {
		rtts[i] = 40 * time.Millisecond
	}
	for i := 5; i < 9; i++ {
		rtts[i] = 0
	}
	s := voiceSummaryOf(rtts)
	if s.Recv != 16 || s.LossRun != 4 {
		t.Fatalf("lost run: %+v", s)
	}
	if s.Gaps60 != 1 || s.Gaps150 != 0 || s.GapMax != 100*time.Millisecond {
		t.Fatalf("lost run gaps: gaps60=%d gaps150=%d max=%v", s.Gaps60, s.Gaps150, s.GapMax)
	}
}

// What a datagram inside TCP does: nothing is lost, but one segment repaired
// late holds the replies behind it, and they arrive together. The loss is zero
// and the call still hears a 200 ms dropout.
func TestVoiceSummarySeesAStallWithNoLoss(t *testing.T) {
	rtts := make([]time.Duration, 30)
	for i := range rtts {
		rtts[i] = 40 * time.Millisecond
	}
	// Requests 10..19 are held until request 10's reply lands 240 ms after it
	// was sent; every one of them arrives at that moment.
	release := 10*voiceInterval + 240*time.Millisecond
	for i := 10; i < 20; i++ {
		rtts[i] = release - time.Duration(i)*voiceInterval
	}
	s := voiceSummaryOf(rtts)
	if s.Recv != 30 || s.LossRun != 0 {
		t.Fatalf("stall lost packets: %+v", s)
	}
	if s.Gaps150 != 1 || s.GapMax != 220*time.Millisecond {
		t.Fatalf("stall: gaps150=%d max=%v, want one 220 ms silence", s.Gaps150, s.GapMax)
	}
	if s.Max != 240*time.Millisecond || s.Jitter == 0 {
		t.Fatalf("stall times: %+v", s)
	}
	line := s.String()
	for _, want := range []string{"sent=30 recv=30 loss=0.00 loss_run=0", "gaps150=1", "gap_max=220", "rtt_max=240"} {
		if !strings.Contains(line, want) {
			t.Fatalf("line %q lacks %q", line, want)
		}
	}
}

func TestVoiceSummaryOfNothingBack(t *testing.T) {
	s := voiceSummaryOf(make([]time.Duration, 10))
	if got := s.String(); got != "sent=10 recv=0 loss=100.00 loss_run=10" {
		t.Fatalf("all lost: %q", got)
	}
}

func TestVoiceSummaryCountsReorder(t *testing.T) {
	rtts := []time.Duration{40 * time.Millisecond, 80 * time.Millisecond, 40 * time.Millisecond, 40 * time.Millisecond}
	// Request 1's reply (sent at 20 ms, back at 100 ms) lands after request 2's
	// (40 ms + 40 ms = 80 ms).
	s := voiceSummaryOf(rtts)
	if s.Reorder != 1 {
		t.Fatalf("reorder %d, want 1: %+v", s.Reorder, s)
	}
}

// stunEcho answers binding requests on loopback like a STUN server, minus the
// ones drop picks.
func stunEcho(t *testing.T, drop func(seq uint32) bool) *net.UDPAddr {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if n < 20 || binary.BigEndian.Uint16(buf[0:]) != 0x0001 {
				continue
			}
			if drop(binary.BigEndian.Uint32(buf[8:12])) {
				continue
			}
			resp := make([]byte, 20)
			binary.BigEndian.PutUint16(resp[0:], 0x0101)
			copy(resp[4:20], buf[4:20])
			_, _ = conn.WriteToUDP(resp, from)
		}
	}()
	return conn.LocalAddr().(*net.UDPAddr)
}

func TestVoiceSeriesAgainstAnEcho(t *testing.T) {
	addr := stunEcho(t, func(seq uint32) bool { return seq%5 == 0 })
	conn, err := net.DialUDP("udp4", nil, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	sent, got, dup, err := voiceSeries(context.Background(), conn, 50, 2*time.Millisecond, 300*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	s := summarizeVoice(sent, got, dup)
	if s.Sent != 50 || s.Recv != 40 || s.LossRun != 1 || s.Dup != 0 {
		t.Fatalf("echo dropping every fifth: %+v", s)
	}
}

func TestVoiceRequestIsAStunBindingOfTheVoiceSize(t *testing.T) {
	var series [8]byte
	copy(series[:], "abcdefgh")
	b := voiceRequest(series, 7)
	if len(b) != voiceSize {
		t.Fatalf("size %d", len(b))
	}
	if binary.BigEndian.Uint16(b[0:]) != 0x0001 || binary.BigEndian.Uint32(b[4:]) != 0x2112A442 {
		t.Fatal("not a binding request with the magic cookie")
	}
	if int(binary.BigEndian.Uint16(b[2:])) != voiceSize-20 {
		t.Fatalf("message length %d", binary.BigEndian.Uint16(b[2:]))
	}
	if binary.BigEndian.Uint32(b[8:]) != 7 || string(b[12:20]) != "abcdefgh" {
		t.Fatal("transaction id does not carry the sequence and series")
	}
	if binary.BigEndian.Uint16(b[20:]) != voiceSoftware || int(binary.BigEndian.Uint16(b[22:])) != voiceSize-24 {
		t.Fatal("padding attribute")
	}
}

// The voice round shares the gate of the other tester rounds and never runs
// beside a load round, in either order.
func TestVoiceRoundGateAndLoadExclusion(t *testing.T) {
	r := New(discard{}, "/nonexistent")
	r.maybeVoice(context.Background())
	if !r.voiceAt.IsZero() {
		t.Fatal("ran with no gate")
	}
	r.SetReachGate(func() bool { return false })
	r.maybeVoice(context.Background())
	if !r.voiceAt.IsZero() {
		t.Fatal("ran with the gate shut")
	}
	r.SetReachGate(func() bool { return true })

	r.loadBusy = true
	r.maybeVoice(context.Background())
	if !r.voiceAt.IsZero() {
		t.Fatal("started beside a load round")
	}
	if lines := r.VoiceNow(); len(lines) != 1 || !strings.Contains(lines[0], "load round is running") {
		t.Fatalf("on request beside a load round: %v", lines)
	}
	r.loadBusy = false

	r.voiceBusy = true
	r.maybeLoad(context.Background())
	if !r.loadAt.IsZero() {
		t.Fatal("a load round started beside a voice round")
	}
	r.voiceBusy = false
}
