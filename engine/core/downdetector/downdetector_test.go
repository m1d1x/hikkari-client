package downdetector

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"NekkoVPN/core/uptime"
)

var t0 = time.Unix(1761800100, 0).Truncate(Window)

func at(sec int64) int64 { return t0.Unix() + sec }

func TestBuild(t *testing.T) {
	hist := map[string][]uptime.Sample{
		"main": {
			{Ts: at(-1), OK: true, RttMs: 999, Src: uptime.SrcTunnel}, // previous window
			{Ts: at(0), OK: true, RttMs: 70, Src: uptime.SrcTunnel},
			{Ts: at(30), OK: true, RttMs: 90, Src: uptime.SrcTunnel},
			{Ts: at(60), OK: true, RttMs: 80, Src: uptime.SrcTunnel},
			{Ts: at(90), OK: false, Src: uptime.SrcTunnel},
			{Ts: at(120), OK: true, RttMs: 5, Src: uptime.SrcTCP},
			{Ts: at(150), OK: true, RttMs: 1},                          // legacy sample, no source
			{Ts: at(300), OK: true, RttMs: 999, Src: uptime.SrcTunnel}, // next window
		},
		"idle": {{Ts: at(10), OK: false, Src: uptime.SrcICMP}},
		"none": {},
	}
	servers := []Server{
		{Profile: "main", ID: "s:b", Proto: "vless-reality"},
		{Profile: "idle", ID: "s:a", Proto: "hysteria2"},
		{Profile: "none", ID: "s:c", Proto: "trojan"},
	}
	r := Build("r-x", Net{ASN: 12389, Country: "ru", Region: "moscow"}, t0, servers, hist)

	want := Report{
		Install: "r-x", ASN: 12389, Country: "ru", Region: "moscow",
		Window: [2]int64{at(0), at(300)},
		Servers: []ServerStat{
			{ID: "s:a", Proto: "hysteria2", Src: uptime.SrcICMP, Fail: 1},
			{ID: "s:b", Proto: "vless-reality", Src: uptime.SrcTCP, OK: 1, RttP50: 5},
			{ID: "s:b", Proto: "vless-reality", Src: uptime.SrcTunnel, OK: 3, Fail: 1, RttP50: 80},
		},
	}
	if !reflect.DeepEqual(r, want) {
		t.Fatalf("got  %+v\nwant %+v", r, want)
	}
}

func TestServerID(t *testing.T) {
	a := ServerID("Example.com", 443, "vless-reality")
	if a != ServerID("example.com", 443, "vless-reality") {
		t.Error("host case must not change the id")
	}
	if a == ServerID("example.com", 8443, "vless-reality") {
		t.Error("port must change the id")
	}
	if !strings.HasPrefix(a, "s:") || strings.Contains(a, "example") {
		t.Errorf("unexpected id %q", a)
	}
}

func newAgent(t *testing.T, servers []Server) *Agent {
	dir := t.TempDir()
	hist := map[string][]uptime.Sample{
		"pub":  {{Ts: at(10), OK: true, RttMs: 50, Src: uptime.SrcTunnel}},
		"priv": {{Ts: at(10), OK: true, RttMs: 60, Src: uptime.SrcTunnel}},
	}
	return New(filepath.Join(dir, "state.json"), filepath.Join(dir, "outbox.json"), Deps{
		Servers: func() []Server { return servers },
		History: func() map[string][]uptime.Sample { return hist },
		Net:     func() Net { return Net{ASN: 1, Country: "ru"} },
	})
}

func TestAgentPrivateNeedsOptIn(t *testing.T) {
	a := newAgent(t, []Server{
		{Profile: "pub", ID: "n:fra-1"},
		{Profile: "priv", ID: "s:x", Private: true},
	})
	a.tick(t0, t0)
	if got := a.LastReport().Servers; len(got) != 1 || got[0].ID != "n:fra-1" {
		t.Fatalf("private server leaked without opt-in: %+v", got)
	}
	_ = a.SetSettings(Settings{Enabled: true, SharePrivate: true})
	a.tick(t0, t0)
	if got := a.LastReport().Servers; len(got) != 2 {
		t.Fatalf("opted-in private server missing: %+v", got)
	}
}

func TestAgentDisableDropsOutbox(t *testing.T) {
	a := newAgent(t, []Server{{Profile: "pub", ID: "n:fra-1"}})
	a.tick(t0, t0)
	if len(a.Outbox()) != 1 {
		t.Fatal("report not queued")
	}
	_ = a.SetSettings(Settings{Enabled: false})
	a.tick(t0, t0)
	if len(a.Outbox()) != 0 || a.LastReport() != nil {
		t.Fatal("disabled agent kept or queued reports")
	}

	// The cleared outbox and the setting survive a restart.
	b := New(a.statePath, a.outboxPath, a.deps)
	b.Load()
	if b.Settings().Enabled || len(b.Outbox()) != 0 {
		t.Fatal("state not persisted")
	}
}

func TestAgentOutboxCapAndRotation(t *testing.T) {
	a := newAgent(t, []Server{{Profile: "pub", ID: "n:fra-1"}})
	for i := 0; i < OutboxMax+5; i++ {
		a.tick(t0, t0)
	}
	if n := len(a.Outbox()); n != OutboxMax {
		t.Fatalf("outbox len %d, want %d", n, OutboxMax)
	}
	first := a.LastReport().Install
	a.tick(t0, t0.Add(InstallTTL-time.Second))
	if a.LastReport().Install != first {
		t.Fatal("install rotated too early")
	}
	a.tick(t0, t0.Add(InstallTTL))
	if a.LastReport().Install == first {
		t.Fatal("install not rotated after TTL")
	}
}
