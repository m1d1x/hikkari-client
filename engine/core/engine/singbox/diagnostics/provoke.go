package diag

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The operator's third question, опыт 0 of the РФ-каскад (plans/2026-11-05-ru-cascade.md,
// «Матрица опыта 0»): which of ТСПУ's attacks are applied to a Russian address,
// and which only to one abroad? Working traffic cannot say: a good profile
// passes both ways. So the client sends stimuli that are known to be cut on the
// way abroad (a blocked SNI, a flow that runs past 16 KB, a burst of source
// ports, a datagram stream) from the physical card, past the tunnel, to
// provoke boxes the operator names (hikkari-control edge/provoke.go): one in
// Russia, one abroad, the same software on both. Each stimulus is tagged in
// its ClientHello (ALPN «hk-<16 hex>») or datagram, and at the end the client
// asks the box what reached it. «Left the client» beside «reached the box»
// says where the stimulus died.
//
// The hello is Go's crypto/tls, not a browser's: the same on both boxes, so it
// cancels out of the comparison, and a stimulus counts only where the box
// abroad shows it cut (the positive control, read by the operator).
//
// Lines name the label, the box's network and the SNI of each stimulus (domains
// stay in the log); addresses do not.

const (
	maxProvokeTargets = 3
	provokeFlows      = 100
	provokeFlowBatch  = 20
	provokeFlowWait   = 3 * time.Second
	provokeHandshake  = 5 * time.Second
	provokeStall      = 5 * time.Second
	provokeSmall      = 64 << 10
	provokeVolume     = 4 << 20
	provokeVolumeCap  = 12 * time.Second
	provokeUpload     = 1 << 20
	provokeALPN       = "hk-"
	provokeMagic      = "HKP1"
	// provokeControlSNI names a site nobody blocks; on the box's own reply it is
	// the negative control of every round.
	provokeControlSNI = "www.microsoft.com"
)

// provokeTLS is one TLS stimulus: an SNI and what to do after the handshake.
type provokeTLS struct {
	name string
	sni  string
	// random: the SNI is a domain made up for this round, on no list, as our
	// own fresh domain would be. Empty sni and not random: no SNI at all.
	random bool
	dl     int64 // bytes to ask the box for
	ul     int64 // bytes to send it
	cap    time.Duration
}

// The SNI stimuli ask «does the censor read the name on this path, and which
// names does it let through»: a big site (control), a name blocked across
// Russia (calibration: cut both ways is expected, passing to a Russian box is
// news), a name on no list, and no name. The volume pair asks whether a flow
// stops near 16 KB, and whether the name matters to that.
var provokeTLSStimuli = []provokeTLS{
	{name: "control", sni: provokeControlSNI, dl: provokeSmall, cap: provokeHandshake + provokeStall},
	{name: "sni-blocked", sni: "www.youtube.com", dl: provokeSmall, cap: provokeHandshake + provokeStall},
	{name: "sni-random", random: true, dl: provokeSmall, cap: provokeHandshake + provokeStall},
	{name: "sni-none", dl: provokeSmall, cap: provokeHandshake + provokeStall},
	{name: "volume", sni: provokeControlSNI, dl: provokeVolume, cap: provokeVolumeCap},
	{name: "volume-random", random: true, dl: provokeVolume, cap: provokeVolumeCap},
	{name: "upload", sni: provokeControlSNI, ul: provokeUpload, cap: provokeVolumeCap},
}

// randomDomain is a .com name of ten letters, new each round.
func randomDomain() string {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = 'a' + b[i]%26
	}
	return string(b) + ".com"
}

// sniWord renders an SNI for the log line: «-» for none.
func sniWord(sni string) string {
	if sni == "" {
		return "-"
	}
	return sni
}

// provokeUDP is one datagram stimulus: a rate, a size and a length.
type provokeUDP struct {
	name  string
	every time.Duration
	size  int
	count int
}

var provokeUDPStimuli = []provokeUDP{
	{name: "udp-voice", every: 20 * time.Millisecond, size: 160, count: 250}, // 5 s of a call
	{name: "udp-rate", every: 2 * time.Millisecond, size: 1200, count: 1000}, // 2 s at 500/s, 4.8 Mbit/s
}

// ProvokeTargets answers the hub's provoke_targets.
func ProvokeTargets(ctx context.Context, params json.RawMessage) []string {
	targets, err := parseAddrTargets(params, maxProvokeTargets)
	if err != nil {
		return []string{fmt.Sprintf("error=%q", err.Error())}
	}
	// As with the flow counts: only on Windows does a socket bound to the card
	// leave past the TUN, and a stimulus through the tunnel would prove nothing.
	if runtime.GOOS != "windows" {
		return []string{fmt.Sprintf("error=%q", "stimuli need Windows, this is "+runtime.GOOS)}
	}
	ifaces := physicalIfaces()
	if len(ifaces) == 0 {
		return []string{`error="no physical interface"`}
	}
	return provokeAll(ctx, ifaces[0].IP, targets)
}

// provokeAll runs every target at once: each is a different box, and the round
// has to fit the command's two minutes.
func provokeAll(ctx context.Context, local net.IP, targets []probeTarget) []string {
	out := make([][]string, len(targets))
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func(i int, t probeTarget) {
			defer wg.Done()
			out[i] = provokeTarget(ctx, local, t)
		}(i, t)
	}
	wg.Wait()
	var lines []string
	for _, l := range out {
		lines = append(lines, l...)
	}
	return lines
}

// tlsResult is what the client saw of one TLS stimulus.
type tlsResult struct {
	stim provokeTLS
	tag  string
	hs   string // ok, timeout, reset, eof, refused, fail
	hsMs int64
	got  int64 // bytes received (DL) or acknowledged by the box (UL)
	end  string
}

type udpResult struct {
	stim udpStim
	sent int
	back int
	err  string
}

type udpStim struct {
	provokeUDP
	tag string
}

// provokeSeen mirrors the box's answer to SEEN.
type provokeSeen struct {
	SNI   string `json:"sni"`
	Hello bool   `json:"hello"`
	In    int64  `json:"in"`
	Out   int64  `json:"out"`
	UDP   int64  `json:"udp"`
}

func provokeTarget(ctx context.Context, local net.IP, t probeTarget) []string {
	host, _, _ := net.SplitHostPort(t.Addr)
	head := fmt.Sprintf("%s net=%s", t.Label, netOf(net.ParseIP(host)))
	lines := []string{fmt.Sprintf("%s %s", head, provokeFlowsLine(ctx, local, t.Addr))}

	var tags []string
	var tlsOut []tlsResult
	made := randomDomain()
	for _, s := range provokeTLSStimuli {
		if ctx.Err() != nil {
			break
		}
		if s.random {
			s.sni = made
		}
		r := runTLSStimulus(ctx, local, t.Addr, s, newTag())
		tags = append(tags, r.tag)
		tlsOut = append(tlsOut, r)
	}
	var udpOut []udpResult
	for _, s := range provokeUDPStimuli {
		if ctx.Err() != nil {
			break
		}
		r := runUDPStimulus(ctx, local, t.Addr, udpStim{s, newTag()})
		tags = append(tags, r.stim.tag)
		udpOut = append(udpOut, r)
	}

	seen, seenErr := askSeen(ctx, local, t.Addr, tags)
	srv := func(tag string) string {
		if seenErr != nil {
			return "srv=?"
		}
		s := seen[tag]
		return fmt.Sprintf("srv_hello=%d srv_sni=%q srv_out=%d srv_in=%d", b2i(s.Hello), s.SNI, s.Out, s.In)
	}
	for _, r := range tlsOut {
		want := r.stim.dl
		if r.stim.ul > 0 {
			want = r.stim.ul
		}
		lines = append(lines, fmt.Sprintf("%s stim=%s sni=%s hs=%s hs_ms=%d got=%d/%d end=%s %s",
			head, r.stim.name, sniWord(r.stim.sni), r.hs, r.hsMs, r.got, want, r.end, srv(r.tag)))
	}
	for _, r := range udpOut {
		line := fmt.Sprintf("%s stim=%s size=%d sent=%d back=%d", head, r.stim.name, r.stim.size, r.sent, r.back)
		if r.err != "" {
			line += fmt.Sprintf(" udp_error=%q", r.err)
		}
		if seenErr != nil {
			line += " srv=?"
		} else {
			line += fmt.Sprintf(" srv_udp=%d", seen[r.stim.tag].UDP)
		}
		lines = append(lines, line)
	}
	if seenErr != nil {
		lines = append(lines, fmt.Sprintf("%s seen=fail reason=%s", head, seenErr))
	} else {
		lines = append(lines, head+" seen=ok")
	}
	return lines
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func newTag() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// provokeFlowsLine opens provokeFlows bare TCP connections in batches, each
// from its own source port, and counts those that opened: the flow loss by
// source port that 12 flows only hint at.
func provokeFlowsLine(ctx context.Context, local net.IP, addr string) string {
	d := net.Dialer{LocalAddr: &net.TCPAddr{IP: local}, Timeout: provokeFlowWait}
	var mu sync.Mutex
	var opened []time.Duration
	refused := 0
	for done := 0; done < provokeFlows && ctx.Err() == nil; done += provokeFlowBatch {
		var wg sync.WaitGroup
		for i := 0; i < provokeFlowBatch; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				start := time.Now()
				c, err := d.DialContext(ctx, "tcp4", addr)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					if classify(err) == "refused" {
						refused++
					}
					return
				}
				opened = append(opened, time.Since(start))
				_ = c.Close()
			}()
		}
		wg.Wait()
	}
	med := "-"
	if len(opened) > 0 {
		sort.Slice(opened, func(i, j int) bool { return opened[i] < opened[j] })
		med = opened[len(opened)/2].Round(time.Millisecond).String()
	}
	return fmt.Sprintf("stim=flows opened=%d/%d refused=%d med=%s", len(opened), provokeFlows, refused, med)
}

// classify names how a connection failed, in the words the plan's table uses.
func classify(err error) string {
	var ne net.Error
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "eof"
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.ECONNABORTED), isWSA(err, wsaReset, wsaAborted):
		return "reset"
	case errors.Is(err, syscall.ECONNREFUSED), isWSA(err, wsaRefused):
		return "refused"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded),
		errors.As(err, &ne) && ne.Timeout():
		return "timeout"
	}
	return "fail"
}

// Winsock's codes. On Windows syscall.ECONNRESET and its kin are Go's own
// numbers, and a socket error carries the WSA one, which errors.Is misses.
const (
	wsaAborted = 10053
	wsaReset   = 10054
	wsaRefused = 10061
)

func isWSA(err error, codes ...uintptr) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	for _, c := range codes {
		if uintptr(errno) == c {
			return true
		}
	}
	return false
}

// stallOr reads a timeout in the middle of a flow as a stall.
func stallOr(err error) string {
	if why := classify(err); why != "timeout" {
		return why
	}
	return "stall"
}

func runTLSStimulus(ctx context.Context, local net.IP, addr string, s provokeTLS, tag string) tlsResult {
	r := tlsResult{stim: s, tag: tag, end: "-"}
	ctx, cancel := context.WithTimeout(ctx, s.cap)
	defer cancel()
	d := net.Dialer{LocalAddr: &net.TCPAddr{IP: local}, Timeout: provokeHandshake}
	raw, err := d.DialContext(ctx, "tcp4", addr)
	if err != nil {
		r.hs = "tcp-" + classify(err)
		return r
	}
	defer raw.Close()
	c := tls.Client(raw, &tls.Config{
		ServerName:         s.sni,
		NextProtos:         []string{provokeALPN + tag},
		InsecureSkipVerify: true, // the box's certificate is self-signed on purpose; what matters is whether the hello passes
	})
	hctx, hcancel := context.WithTimeout(ctx, provokeHandshake)
	start := time.Now()
	err = c.HandshakeContext(hctx)
	hcancel()
	r.hsMs = time.Since(start).Milliseconds()
	if r.hs = classify(err); err != nil {
		return r
	}
	stop := context.AfterFunc(ctx, func() { _ = raw.SetDeadline(time.Now()) })
	defer stop()
	if s.ul > 0 {
		r.got, r.end = uploadStimulus(c, raw, s.ul)
	} else {
		r.got, r.end = downloadStimulus(c, raw, s.dl)
	}
	return r
}

// downloadStimulus asks the box for n bytes and reads until they all come, the
// flow stalls for provokeStall, or it breaks. got is where it stopped: a stop
// near 16 KB is the cut by volume.
func downloadStimulus(c *tls.Conn, raw net.Conn, n int64) (int64, string) {
	_ = raw.SetDeadline(time.Now().Add(provokeStall))
	if _, err := fmt.Fprintf(c, "DL %d\n", n); err != nil {
		return 0, classify(err)
	}
	buf := make([]byte, 32<<10)
	var got int64
	for got < n {
		_ = raw.SetReadDeadline(time.Now().Add(provokeStall))
		k, err := c.Read(buf)
		got += int64(k)
		if err != nil {
			return got, stallOr(err)
		}
	}
	return got, "done"
}

// uploadStimulus sends n bytes and returns what the box says arrived.
func uploadStimulus(c *tls.Conn, raw net.Conn, n int64) (int64, string) {
	_ = raw.SetDeadline(time.Now().Add(provokeStall))
	if _, err := fmt.Fprintf(c, "UL %d\n", n); err != nil {
		return 0, classify(err)
	}
	chunk := make([]byte, 32<<10)
	for sent := int64(0); sent < n; {
		w := int64(len(chunk))
		if n-sent < w {
			w = n - sent
		}
		_ = raw.SetWriteDeadline(time.Now().Add(provokeStall))
		k, err := c.Write(chunk[:w])
		sent += int64(k)
		if err != nil {
			return 0, stallOr(err)
		}
	}
	_ = raw.SetReadDeadline(time.Now().Add(provokeStall))
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		return 0, classify(err)
	}
	got, err := strconv.ParseInt(strings.TrimPrefix(strings.TrimSpace(line), "OK "), 10, 64)
	if err != nil {
		return 0, "fail"
	}
	if got < n {
		return got, "short"
	}
	return got, "done"
}

// runUDPStimulus sends tagged datagrams at the stimulus' rate and counts those
// that come back; a reader counts while the sender paces.
func runUDPStimulus(ctx context.Context, local net.IP, addr string, s udpStim) udpResult {
	r := udpResult{stim: s}
	raddr, err := net.ResolveUDPAddr("udp4", addr)
	if err != nil {
		r.err = "address"
		return r
	}
	conn, err := net.DialUDP("udp4", &net.UDPAddr{IP: local}, raddr)
	if err != nil {
		r.err = classify(err)
		return r
	}
	defer conn.Close()
	tag, _ := hex.DecodeString(s.tag)
	size := max(s.size, len(provokeMagic)+12)
	packet := make([]byte, size)
	copy(packet, provokeMagic)
	copy(packet[len(provokeMagic):], tag)

	var back int
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 2048)
		for {
			k, err := conn.Read(buf)
			if err != nil {
				// An ICMP unreachable surfaces as one read error on a connected
				// socket; the stream goes on. Only the deadline or close ends it.
				if why := classify(err); why == "timeout" || errors.Is(err, net.ErrClosed) {
					return
				}
				continue
			}
			if k == size && string(buf[:len(provokeMagic)+8]) == string(packet[:len(provokeMagic)+8]) {
				back++
			}
		}
	}()
	tick := time.NewTicker(s.every)
	for i := 0; i < s.count && ctx.Err() == nil; i++ {
		binary.BigEndian.PutUint32(packet[len(provokeMagic)+8:], uint32(i))
		if _, err := conn.Write(packet); err == nil {
			r.sent++
		} else if r.err == "" {
			r.err = classify(err)
		}
		<-tick.C
	}
	tick.Stop()
	// the last echoes are a round trip behind the last send
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	<-readDone
	r.back = back
	return r
}

// askSeen asks the box, over a control-SNI connection, what reached it of each
// tag. Its failure leaves only the client's half of the picture.
func askSeen(ctx context.Context, local net.IP, addr string, tags []string) (map[string]provokeSeen, error) {
	ctx, cancel := context.WithTimeout(ctx, provokeHandshake+provokeStall)
	defer cancel()
	d := net.Dialer{LocalAddr: &net.TCPAddr{IP: local}, Timeout: provokeHandshake}
	raw, err := d.DialContext(ctx, "tcp4", addr)
	if err != nil {
		return nil, errors.New("tcp-" + classify(err))
	}
	defer raw.Close()
	c := tls.Client(raw, &tls.Config{ServerName: provokeControlSNI, NextProtos: []string{provokeALPN + newTag()}, InsecureSkipVerify: true})
	if err := c.HandshakeContext(ctx); err != nil {
		return nil, errors.New("hs-" + classify(err))
	}
	_ = raw.SetDeadline(time.Now().Add(provokeStall))
	if _, err := fmt.Fprintf(c, "SEEN %s\n", strings.Join(tags, ",")); err != nil {
		return nil, errors.New("write-" + classify(err))
	}
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		return nil, errors.New("read-" + classify(err))
	}
	var seen map[string]provokeSeen
	if err := json.Unmarshal([]byte(line), &seen); err != nil {
		return nil, errors.New("answer")
	}
	return seen, nil
}
