package netdoctor

import (
	"errors"
	"net"
	"reflect"
	"strconv"
	"testing"
)

func TestParseServiceProxy(t *testing.T) {
	got := parseServiceProxy("Enabled: Yes\nServer: 127.0.0.1\nPort: 7890\nAuthenticated Proxy Enabled: 0\n")
	if want := (ProxySetting{Enabled: true, Server: "127.0.0.1:7890"}); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if got := parseServiceProxy("Enabled: No\nServer: \nPort: 0\nAuthenticated Proxy Enabled: 0\n"); got.Enabled || got.Server != "" {
		t.Fatalf("disabled and empty: got %+v", got)
	}
}

type fakeProxies struct {
	reads    [][]ServiceProxy
	disabled []string
	failOn   string
}

func (f *fakeProxies) list() ([]ServiceProxy, error) {
	r := f.reads[0]
	if len(f.reads) > 1 {
		f.reads = f.reads[1:]
	}
	return r, nil
}

func (f *fakeProxies) disable(service, kind string) error {
	if service == f.failOn {
		return errors.New("denied")
	}
	f.disabled = append(f.disabled, service+"/"+kind)
	return nil
}

func proxyOn(service, kind, server string) ServiceProxy {
	return ServiceProxy{Service: service, Kind: kind, Setting: ProxySetting{Enabled: true, Server: server}}
}

// deadPort is a loopback port nothing listens on.
func deadPort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

func livePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l.Addr().String()
}

func TestRepairProxiesDisablesOnlyOrphanedServices(t *testing.T) {
	dead, live := deadPort(t), livePort(t)
	list := []ServiceProxy{
		proxyOn("Wi-Fi", "web", dead),
		proxyOn("Wi-Fi", "socks", dead),
		{Service: "Wi-Fi", Kind: "secure"},
		proxyOn("Ethernet", "web", live),
		proxyOn("Office", "web", "10.0.0.5:3128"),
	}
	store := &fakeProxies{reads: [][]ServiceProxy{list}}
	a, err := repairProxies(store)
	if err != nil {
		t.Fatal(err)
	}
	if !a.Done {
		t.Fatalf("action: %+v", a)
	}
	if want := []string{"Wi-Fi/web", "Wi-Fi/socks"}; !reflect.DeepEqual(store.disabled, want) {
		t.Fatalf("disabled %v, want %v", store.disabled, want)
	}
}

// One live endpoint means the tool still runs: its other, dead, endpoint is
// not a leftover.
func TestOneLiveEndpointKeepsTheWholeService(t *testing.T) {
	store := &fakeProxies{reads: [][]ServiceProxy{{
		proxyOn("Wi-Fi", "web", deadPort(t)),
		proxyOn("Wi-Fi", "socks", livePort(t)),
	}}}
	a, err := repairProxies(store)
	if err != nil {
		t.Fatal(err)
	}
	if a.Done || len(store.disabled) != 0 {
		t.Fatalf("touched a live service: %+v, %v", a, store.disabled)
	}
}

func TestRepairProxiesLeavesAChangedSettingAlone(t *testing.T) {
	dead := deadPort(t)
	store := &fakeProxies{reads: [][]ServiceProxy{
		{proxyOn("Wi-Fi", "web", dead)},
		{proxyOn("Wi-Fi", "web", "127.0.0.1:1")},
	}}
	a, err := repairProxies(store)
	if err != nil {
		t.Fatal(err)
	}
	if a.Done || len(store.disabled) != 0 {
		t.Fatalf("wrote over a changed setting: %+v, %v", a, store.disabled)
	}
}

func TestRepairProxiesReportsAFailedWrite(t *testing.T) {
	store := &fakeProxies{failOn: "Wi-Fi", reads: [][]ServiceProxy{{proxyOn("Wi-Fi", "web", deadPort(t))}}}
	a, err := repairProxies(store)
	if err != nil {
		t.Fatal(err)
	}
	if a.Done || a.Detail == "" {
		t.Fatalf("a failed write must not read as done: %+v", a)
	}
}

func TestDeadLoopbackDNS(t *testing.T) {
	dead := func(string) bool { return false }
	alive := func(string) bool { return true }
	for _, tc := range []struct {
		name    string
		servers []string
		alive   func(string) bool
		want    bool
	}{
		{"dhcp", nil, dead, false},
		{"dead stub", []string{"127.0.0.1"}, dead, true},
		{"live stub", []string{"127.0.0.1"}, alive, false},
		{"remote", []string{"1.1.1.1"}, dead, false},
		{"stub and remote", []string{"127.0.0.1", "1.1.1.1"}, dead, false},
	} {
		if got := deadLoopbackDNS(tc.servers, tc.alive); got != tc.want {
			t.Errorf("%s: got %v", tc.name, got)
		}
	}
}

// A stub that refuses a name is still running; a closed port is not.
func TestDNSStubAlive(t *testing.T) {
	if testing.Short() {
		t.Skip("needs port 53")
	}
	conn, err := net.ListenPacket("udp", "127.0.0.2:53")
	if err != nil {
		t.Skipf("cannot bind port 53: %v", err)
	}
	defer conn.Close()
	go func() {
		b := make([]byte, 512)
		n, from, err := conn.ReadFrom(b)
		if err == nil {
			b[3] |= 0x02 // SERVFAIL
			_, _ = conn.WriteTo(b[:n], from)
		}
	}()
	if !dnsStubAlive("127.0.0.2") {
		t.Fatal("a stub answering SERVFAIL reads as dead")
	}
}

func TestParseResolvers(t *testing.T) {
	const out = `DNS configuration

resolver #1
  search domain[0] : corp.example
  nameserver[0] : 172.20.10.1
  nameserver[1] : fe80::1
  if_index : 10 (en6)
  flags    : Request A records
  order    : 200000

resolver #2
  domain   : local
  options  : mdns
  timeout  : 5
  order    : 300000

resolver #3
  domain   : internal.example
  nameserver[0] : 10.0.0.53
  order    : 1

DNS configuration (for scoped queries)

resolver #1
  nameserver[0] : 172.20.10.1
  if_index : 10 (en6)
  flags    : Scoped, Request A records

resolver #2
  nameserver[0] : 192.168.1.1
  nameserver[1] : 8.8.8.8
  if_index : 4 (en0)
  flags    : Scoped, Request A records
`
	global, scoped := parseResolvers(out)
	if want := []string{"172.20.10.1"}; !reflect.DeepEqual(global, want) {
		t.Fatalf("global %v, want %v", global, want)
	}
	want := map[int][]string{10: {"172.20.10.1"}, 4: {"192.168.1.1", "8.8.8.8"}}
	if !reflect.DeepEqual(scoped, want) {
		t.Fatalf("scoped %v, want %v", scoped, want)
	}
}

func TestAtoiOrZero(t *testing.T) {
	for in, want := range map[string]int{"10": 10, "0": 0, "x1": 0, strconv.Itoa(4): 4} {
		if got := atoiOrZero(in); got != want {
			t.Errorf("%q: got %d", in, got)
		}
	}
}
