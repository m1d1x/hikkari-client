package netdoctor

import (
	"net"
	"reflect"
	"testing"
)

func TestParseProxyServerAcceptsBothWindowsForms(t *testing.T) {
	cases := map[string][]string{
		"127.0.0.1:7890": {"127.0.0.1:7890"},
		"http=127.0.0.1:10808;https=127.0.0.1:10808;socks=127.0.0.1:10809": {"127.0.0.1:10808", "127.0.0.1:10809"},
		"proxy.corp":             {"proxy.corp:80"},
		"http://localhost:8080/": {"localhost:8080"},
		"[::1]:1080":             {"[::1]:1080"},
		"":                       nil,
	}
	for in, want := range cases {
		if got := ParseProxyServer(in); !reflect.DeepEqual(got, want) {
			t.Errorf("ParseProxyServer(%q) = %v, want %v", in, got, want)
		}
	}
}

func withProbe(t *testing.T, live map[string]bool) {
	t.Helper()
	old := probe
	probe = func(hp string) bool { return live[hp] }
	t.Cleanup(func() { probe = old })
}

func TestClassify(t *testing.T) {
	withProbe(t, map[string]bool{"127.0.0.1:7890": true})
	cases := []struct {
		s    ProxySetting
		want string
	}{
		{ProxySetting{Enabled: false, Server: "127.0.0.1:1"}, ProxyNone},
		{ProxySetting{Enabled: true}, ProxyNone},
		{ProxySetting{Enabled: true, Server: "127.0.0.1:7890"}, ProxyLive},
		{ProxySetting{Enabled: true, Server: "127.0.0.1:10808"}, ProxyOrphan},
		{ProxySetting{Enabled: true, Server: "localhost:10808"}, ProxyOrphan},
		// One live endpoint means the tool still runs.
		{ProxySetting{Enabled: true, Server: "http=127.0.0.1:10808;socks=127.0.0.1:7890"}, ProxyLive},
		// Any remote endpoint means it is not a local leftover.
		{ProxySetting{Enabled: true, Server: "http=127.0.0.1:10808;https=10.0.0.5:3128"}, ProxyRemote},
		{ProxySetting{Enabled: true, Server: "proxy.corp:3128"}, ProxyRemote},
	}
	for _, c := range cases {
		if got := Classify(c.s).Verdict; got != c.want {
			t.Errorf("Classify(%+v) = %s, want %s", c.s, got, c.want)
		}
	}
}

func TestProbeSeesARealListenerAndADeadPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if !probe(addr) {
		t.Fatalf("probe(%s) = false with a listener", addr)
	}
	ln.Close()
	if probe(addr) {
		t.Fatalf("probe(%s) = true after close", addr)
	}
}
