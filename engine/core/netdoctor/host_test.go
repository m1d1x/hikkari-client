package netdoctor

import (
	"encoding/binary"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeEnv struct {
	cs       []Candidate
	bestIdx  int
	reachOK  map[int]bool
	dnsOK    map[string]bool // server -> answers
	failOnce map[string]bool // server fails the first round only
	rounds   map[string]int
	probed   []string // targets touched
}

func (f *fakeEnv) candidates() ([]Candidate, error) {
	if f.cs == nil {
		return nil, errors.New("none")
	}
	return f.cs, nil
}
func (f *fakeEnv) best(string) (int, error) { return f.bestIdx, nil }
func (f *fakeEnv) reach(idx int, target string) bool {
	f.probed = append(f.probed, target)
	return f.reachOK[idx]
}
func (f *fakeEnv) resolve(idx int, server, domain string) bool {
	f.probed = append(f.probed, server)
	if f.failOnce[server] {
		if f.rounds == nil {
			f.rounds = map[string]int{}
		}
		f.rounds[server]++
		return f.rounds[server] > len(probeDomains)
	}
	return f.dnsOK[server]
}
func (f *fakeEnv) sleep(time.Duration) {}

var (
	wifi = Candidate{Index: 7, Name: "Wi-Fi", Metric: 35, Gateway: "192.168.1.1", DNS: []string{"192.168.1.1"}, Physical: true}
	tap  = Candidate{Index: 12, Name: "TAP-Windows", Metric: 1, Gateway: "10.8.0.1", DNS: []string{"10.8.0.1"}}
)

func TestTheInterfaceWindowsPicksIsKeptWhenItWorks(t *testing.T) {
	env := &fakeEnv{cs: []Candidate{tap, wifi}, bestIdx: 7, reachOK: map[int]bool{7: true}, dnsOK: map[string]bool{"192.168.1.1": true}}
	s := selectHost(env)
	if s.Iface != IfaceAuto || s.DNS != DNSSystem || !s.Default() {
		t.Fatalf("%+v", s)
	}
}

func TestADeadVirtualAdapterIsBypassedByPinningAWorkingOne(t *testing.T) {
	env := &fakeEnv{cs: []Candidate{tap, wifi}, bestIdx: 12, reachOK: map[int]bool{7: true}, dnsOK: map[string]bool{"192.168.1.1": true}}
	s := selectHost(env)
	if s.Iface != IfacePinned || s.DirectInterface != "Wi-Fi" || s.DNS != DNSSystem {
		t.Fatalf("%+v", s)
	}
}

func TestADeadLoopbackResolverFallsBack(t *testing.T) {
	w := wifi
	w.DNS = []string{"127.0.0.1"}
	env := &fakeEnv{cs: []Candidate{w}, bestIdx: 7, reachOK: map[int]bool{7: true}, dnsOK: map[string]bool{FallbackDNS: true}}
	s := selectHost(env)
	if s.DNS != DNSFallback || s.DirectDNS != FallbackDNS || s.DirectInterface != "" {
		t.Fatalf("%+v", s)
	}
}

func TestOneHiccupDoesNotSwitchTheResolver(t *testing.T) {
	env := &fakeEnv{cs: []Candidate{wifi}, bestIdx: 7, reachOK: map[int]bool{7: true}, failOnce: map[string]bool{"192.168.1.1": true}}
	if s := selectHost(env); s.DNS != DNSSystem || s.DirectDNS != "" {
		t.Fatalf("%+v", s)
	}
}

func TestOfflineLeavesEverythingAsIs(t *testing.T) {
	env := &fakeEnv{cs: []Candidate{wifi}, bestIdx: 7, reachOK: map[int]bool{}}
	if s := selectHost(env); s.Iface != IfaceUnreachable || !s.Default() {
		t.Fatalf("%+v", s)
	}
	env = &fakeEnv{cs: []Candidate{wifi}, bestIdx: 7, reachOK: map[int]bool{7: true}}
	if s := selectHost(env); s.DNS != DNSUnreachable || !s.Default() {
		t.Fatalf("%+v", s)
	}
}

// The censorship-safety contract: probes touch only the neutral targets
// and the adapter's own resolvers.
func TestProbesNeverLeaveTheNeutralTargets(t *testing.T) {
	env := &fakeEnv{cs: []Candidate{tap, wifi}, bestIdx: 12, reachOK: map[int]bool{7: true}, dnsOK: map[string]bool{}}
	selectHost(env)
	allowed := map[string]bool{"192.168.1.1": true}
	for _, t := range ProbeTargets {
		allowed[t] = true
	}
	for _, p := range env.probed {
		if !allowed[p] {
			t.Fatalf("probe went to %s", p)
		}
	}
}

func TestNetworkKeyIgnoresOrder(t *testing.T) {
	if networkKey([]Candidate{tap, wifi}) != networkKey([]Candidate{wifi, tap}) {
		t.Fatal("order-dependent key")
	}
}

func TestQueryAndAnswer(t *testing.T) {
	q, err := buildQuery(0xbeef, "ya.ru")
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0xbe, 0xef, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 2, 'y', 'a', 2, 'r', 'u', 0, 0, 1, 0, 1}
	if string(q) != string(want) {
		t.Fatalf("query % x", q)
	}
	resp := append([]byte(nil), q...)
	binary.BigEndian.PutUint16(resp[2:], 0x8180)
	binary.BigEndian.PutUint16(resp[6:], 1)
	if !answered(resp, 0xbeef) {
		t.Fatal("good answer rejected")
	}
	binary.BigEndian.PutUint16(resp[2:], 0x8183) // NXDOMAIN
	if answered(resp, 0xbeef) || answered(resp[:5], 0xbeef) {
		t.Fatal("bad answer accepted")
	}
	if _, err := buildQuery(1, "a..b"); err == nil {
		t.Fatal("empty label accepted")
	}
}

func TestDetailsExplainAPinnedFallbackWithoutNamesOrAddresses(t *testing.T) {
	w := wifi
	w.DNS = []string{"127.0.0.1"}
	env := &fakeEnv{cs: []Candidate{tap, w}, bestIdx: 12, reachOK: map[int]bool{7: true}, dnsOK: map[string]bool{FallbackDNS: true}}
	s := selectHost(env)
	d := s.Details
	if d.Candidates != 2 || d.Physical != 1 || d.Virtual != 1 || d.BestKind != "virtual" || d.BestOK ||
		d.OthersReached != 1 || d.SystemDNSOK || !d.FallbackOK || len(d.ResolverKinds) != 1 || d.ResolverKinds[0] != "loopback" {
		t.Fatalf("%+v", d)
	}
	line := s.LogLine()
	for _, leak := range []string{"Wi-Fi", "TAP", "127.0.0.1", "192.168", "10.8"} {
		if strings.Contains(line, leak) {
			t.Fatalf("log line leaks %q: %s", leak, line)
		}
	}
	if !strings.Contains(line, "best=virtual:fail") || !strings.Contains(line, "resolvers=loopback") {
		t.Fatalf("log line: %s", line)
	}
}
