package diag

import (
	"context"
	"net"
	"strings"
	"testing"
)

func TestReachTargetsTellTCPFromUDP(t *testing.T) {
	config := []byte(`{
		"outbounds": [
			{"tag": "direct", "type": "direct"},
			{"tag": "p-001", "type": "vless", "server": "192.0.2.1", "server_port": 443, "tls": {"reality": {"enabled": true}}},
			{"tag": "p-003", "type": "hysteria2", "server": "192.0.2.3", "server_port": 443},
			{"tag": "proxy", "type": "selector"}
		],
		"endpoints": [{"tag": "p-awg", "type": "wireguard"}]
	}`)
	got, err := reachTargets(config)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("targets = %+v, want the three profiles", got)
	}
	want := map[string]bool{"p-001": false, "p-003": true, "p-awg": true}
	for _, target := range got {
		if target.UDP != want[target.Tag] {
			t.Errorf("%s: udp = %v", target.Tag, target.UDP)
		}
		if target.Tag == "p-001" && target.Proto != "vless-reality" {
			t.Errorf("p-001: proto = %q", target.Proto)
		}
	}
}

func TestCountFlowsCountsWhatOpened(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	local := net.ParseIP("127.0.0.1")
	if through, _ := countFlows(context.Background(), local, addr); through != reachFlows {
		t.Fatalf("open port: %d/%d flows", through, reachFlows)
	}
	_ = ln.Close()
	if through, med := countFlows(context.Background(), local, addr); through != 0 || med != "-" {
		t.Fatalf("closed port: %d flows, median %s", through, med)
	}
}

func TestReachRunsOnlyWhenAllowed(t *testing.T) {
	r := New(discard{}, "/nonexistent")
	r.maybeReach(context.Background())
	if !r.reachAt.IsZero() {
		t.Fatal("ran with no gate")
	}
	r.SetReachGate(func() bool { return false })
	r.maybeReach(context.Background())
	if !r.reachAt.IsZero() {
		t.Fatal("ran with the gate shut")
	}
	r.SetReachGate(func() bool { return true })
	r.maybeReach(context.Background())
	if r.reachAt.IsZero() {
		t.Fatal("did not run with the gate open")
	}
}

type discard struct{}

func (discard) Logf(string, ...interface{}) {}

// A Hikkari profile with further entries (v1.07) has each counted on its own
// line part: by number and network, never by address.
func TestReachLineCountsEveryEntry(t *testing.T) {
	config := []byte(`{"outbounds": [{"tag": "p-hk", "type": "hikkari", "server": "127.0.0.1", "server_port": 1,
		"entries": [{"server": "127.0.0.1", "server_port": 2}, {"server": "entry.example", "server_port": 3}]}]}`)
	targets, err := reachTargets(config)
	if err != nil || len(targets) != 1 {
		t.Fatalf("targets %+v, %v", targets, err)
	}
	got := targets[0].Entries
	if len(got) != 2 || got[0] != "127.0.0.1:2" || got[1] != "entry.example:3" {
		t.Fatalf("entries %q", got)
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	target := targets[0]
	target.Entries[0] = ln.Addr().String()
	line := (&Runner{}).reachLine(context.Background(), target, []physIface{{Name: "eth", IP: net.ParseIP("127.0.0.1")}})
	if !strings.Contains(line, " entry1 net=") || !strings.Contains(line, "entry1.flows[eth]=12/12") {
		t.Fatalf("line %q", line)
	}
	if strings.Contains(line, "entry2") || strings.Contains(line, ln.Addr().String()) {
		t.Fatalf("line names a name-only entry or an address: %q", line)
	}
}
