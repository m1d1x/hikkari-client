package diag

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeBox is a test stand-in for hikkari-control edge/provoke.go: the same wire,
// plus a censor in the middle that can be told to cut a stimulus the way ТСПУ
// does. It is a fake, not the box: the box's own tests live with the box.
type fakeBox struct {
	mu   sync.Mutex
	seen map[string]*provokeSeen
	// cutSNI drops the connection right after the hello for these SNIs: the box
	// saw the hello, the client never got the ServerHello.
	cutSNI map[string]bool
	// freezeAfter stops the volume download after this many bytes and holds the
	// connection open, as the cut by volume does. 0: no freeze.
	freezeAfter int64
	// dropUDP drops every datagram before the box.
	dropUDP bool
}

func (b *fakeBox) rec(tag string) *provokeSeen {
	if b.seen[tag] == nil {
		b.seen[tag] = &provokeSeen{}
	}
	return b.seen[tag]
}

func startFakeBox(t *testing.T, b *fakeBox) string {
	t.Helper()
	b.seen = map[string]*provokeSeen{}
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "box"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	cfg := &tls.Config{GetConfigForClient: func(hi *tls.ClientHelloInfo) (*tls.Config, error) {
		c := &tls.Config{Certificates: []tls.Certificate{cert}}
		for _, p := range hi.SupportedProtos {
			if tag, ok := strings.CutPrefix(p, provokeALPN); ok {
				b.mu.Lock()
				r := b.rec(tag)
				r.Hello, r.SNI = true, hi.ServerName
				b.mu.Unlock()
				c.NextProtos = []string{p}
			}
		}
		if b.cutSNI[hi.ServerName] {
			return nil, fmt.Errorf("cut")
		}
		return c, nil
	}}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ua, _ := net.ResolveUDPAddr("udp", ln.Addr().String())
	ul, err := net.ListenUDP("udp", ua)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close(); ul.Close() })
	go func() {
		for {
			raw, err := ln.Accept()
			if err != nil {
				return
			}
			go b.serve(raw, cfg)
		}
	}()
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := ul.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if b.dropUDP || n < 16 || string(buf[:4]) != provokeMagic {
				continue
			}
			b.mu.Lock()
			b.rec(hex.EncodeToString(buf[4:12])).UDP++
			b.mu.Unlock()
			_, _ = ul.WriteToUDP(buf[:n], from)
		}
	}()
	return ln.Addr().String()
}

func (b *fakeBox) serve(raw net.Conn, cfg *tls.Config) {
	defer raw.Close()
	c := tls.Server(raw, cfg)
	if err := c.Handshake(); err != nil {
		return
	}
	tag := strings.TrimPrefix(c.ConnectionState().NegotiatedProtocol, provokeALPN)
	r := bufio.NewReader(c)
	line, err := r.ReadString('\n')
	if err != nil {
		return
	}
	verb, arg, _ := strings.Cut(strings.TrimSpace(line), " ")
	switch verb {
	case "DL":
		n, _ := strconv.ParseInt(arg, 10, 64)
		if b.freezeAfter > 0 && n > provokeSmall { // only the volume stimulus runs that long
			_, _ = c.Write(make([]byte, b.freezeAfter))
			b.mu.Lock()
			b.rec(tag).Out += b.freezeAfter
			b.mu.Unlock()
			time.Sleep(provokeStall + time.Second)
			return
		}
		_, _ = c.Write(make([]byte, n))
		b.mu.Lock()
		b.rec(tag).Out += n
		b.mu.Unlock()
	case "UL":
		n, _ := strconv.ParseInt(arg, 10, 64)
		got, _ := io.CopyN(io.Discard, r, n)
		b.mu.Lock()
		b.rec(tag).In += got
		b.mu.Unlock()
		fmt.Fprintf(c, "OK %d\n", got)
	case "SEEN":
		out := map[string]provokeSeen{}
		b.mu.Lock()
		for _, t := range strings.Split(arg, ",") {
			if s := b.seen[t]; s != nil {
				out[t] = *s
			} else {
				out[t] = provokeSeen{}
			}
		}
		b.mu.Unlock()
		data, _ := json.Marshal(out)
		_, _ = c.Write(append(data, '\n'))
	}
}

func lineFor(t *testing.T, lines []string, stim string) string {
	t.Helper()
	for _, l := range lines {
		if strings.Contains(l, " stim="+stim+" ") {
			return l
		}
	}
	t.Fatalf("no line for %s in:\n%s", stim, strings.Join(lines, "\n"))
	return ""
}

func mustHave(t *testing.T, line string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(line, p) {
			t.Errorf("%q lacks %q", line, p)
		}
	}
}

func TestProvokeCleanBoxPassesEveryStimulus(t *testing.T) {
	if testing.Short() {
		t.Skip("sends 7 s of datagrams")
	}
	addr := startFakeBox(t, &fakeBox{})
	lines := provokeTarget(context.Background(), net.IPv4(127, 0, 0, 1), probeTarget{Label: "ru-spb", Addr: addr})
	mustHave(t, lineFor(t, lines, "flows"), "opened=100/100", "refused=0")
	mustHave(t, lineFor(t, lines, "control"), "hs=ok", "got=65536/65536", "end=done", "srv_hello=1", `srv_sni="www.microsoft.com"`, "srv_out=65536")
	mustHave(t, lineFor(t, lines, "sni-blocked"), "sni=www.youtube.com", "hs=ok", "end=done", `srv_sni="www.youtube.com"`)
	mustHave(t, lineFor(t, lines, "sni-none"), "sni=- ", "hs=ok", "end=done", `srv_sni=""`)
	random := lineFor(t, lines, "sni-random")
	mustHave(t, random, ".com hs=ok", "end=done")
	name := strings.Fields(strings.SplitAfter(random, " sni=")[1])[0]
	mustHave(t, lineFor(t, lines, "volume-random"), "sni="+name+" ", "got=4194304/4194304")
	mustHave(t, lineFor(t, lines, "volume"), "got=4194304/4194304", "end=done")
	mustHave(t, lineFor(t, lines, "upload"), "got=1048576/1048576", "end=done", "srv_in=1048576")
	mustHave(t, lineFor(t, lines, "udp-voice"), "sent=250", "back=250", "srv_udp=250")
	mustHave(t, lines[len(lines)-1], "seen=ok")
}

func TestProvokeSeesWhereTheCensorCut(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out a stall and sends datagrams")
	}
	box := &fakeBox{cutSNI: map[string]bool{"www.youtube.com": true}, freezeAfter: 16 << 10, dropUDP: true}
	addr := startFakeBox(t, box)
	lines := provokeTarget(context.Background(), net.IPv4(127, 0, 0, 1), probeTarget{Label: "ams3", Addr: addr})
	// The hello reached the box, the client got no handshake.
	yt := lineFor(t, lines, "sni-blocked")
	mustHave(t, yt, "srv_hello=1", "got=0/65536")
	if strings.Contains(yt, "hs=ok") {
		t.Errorf("cut hello read as a handshake: %s", yt)
	}
	// The other SNIs pass.
	mustHave(t, lineFor(t, lines, "sni-random"), "hs=ok", "end=done")
	// A download frozen at 16 KB stalls there.
	mustHave(t, lineFor(t, lines, "volume"), "got=16384/4194304", "end=stall", "srv_out=16384")
	mustHave(t, lineFor(t, lines, "volume-random"), "got=16384/4194304", "end=stall")
	// Datagrams that never reached the box.
	mustHave(t, lineFor(t, lines, "udp-voice"), "back=0", "srv_udp=0")
}

func TestProvokeDeadBoxSaysSoWithoutServerNumbers(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close() // nothing listens: every connection is refused
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// TCP-only part: the datagram stimuli run too, and with nothing on the port
	// they come back empty.
	lines := provokeTarget(ctx, net.IPv4(127, 0, 0, 1), probeTarget{Label: "dead", Addr: addr})
	mustHave(t, lineFor(t, lines, "flows"), "opened=0/100", "refused=100")
	mustHave(t, lineFor(t, lines, "control"), "hs=tcp-refused", "srv=?")
	mustHave(t, lines[len(lines)-1], "seen=fail", "reason=tcp-refused")
}

func TestParseProvokeTargetsCapsAtThree(t *testing.T) {
	four := `{"targets":[{"label":"a","addr":"1.2.3.4:443"},{"label":"b","addr":"1.2.3.5:443"},{"label":"c","addr":"1.2.3.6:443"},{"label":"d","addr":"1.2.3.7:443"}]}`
	if _, err := parseAddrTargets(json.RawMessage(four), maxProvokeTargets); err == nil {
		t.Fatal("four targets accepted")
	}
	if _, err := parseAddrTargets(json.RawMessage(`{"targets":[{"label":"ru","addr":"10.1.1.1:443"}]}`), maxProvokeTargets); err == nil {
		t.Fatal("a private address accepted")
	}
}
