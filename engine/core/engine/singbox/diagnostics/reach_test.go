package diag

import (
	"context"
	"net"
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
