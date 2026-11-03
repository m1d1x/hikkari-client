package singbox

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"NekkoVPN/core/engine"
	configfile "NekkoVPN/core/engine/singbox/config"
	vpn "NekkoVPN/core/engine/singbox/process"
	"NekkoVPN/core/profile"
	"NekkoVPN/core/routing"
)

type recordLog struct{ lines []string }

func (l *recordLog) Logf(format string, args ...interface{}) {
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}
func (l *recordLog) PipeLines(string, io.ReadCloser) {}

// brokenAwg is how 1.0.34 kept an Amnezia profile with a key the core refuses:
// "unknown" stands for any field the core does not know.
func brokenAwg() profile.Profile {
	return profile.Profile{Name: "amnezia", Label: "Amnezia Premium", Kind: profile.KindEndpoint, Config: map[string]interface{}{
		"type": "awg", "private_key": "6Mf9KmZ3Nn4bq2oXQyQbF0jv3bB4i0cVZ0Xh1q3xXkE=", "address": []interface{}{"10.8.1.7/32"},
		"jc": 4, "jmin": 40, "jmax": 70, "s1": 33, "s2": 57, "h1": "11", "h2": "12", "h3": "13", "h4": "14",
		"unknown": "x",
		"peers": []interface{}{map[string]interface{}{
			"address": "203.0.113.9", "port": 41202, "public_key": "lTmjpHaGYBYQ8T5oogAWheEzhjCnzFwvoaZYQevHMgQ=", "allowed_ips": []interface{}{"0.0.0.0/0"},
		}},
	}}
}

func finka() profile.Profile {
	return profile.Profile{Name: "finka", Label: "ФИНКА", Kind: profile.KindOutbound, Config: map[string]interface{}{
		"type": "vless", "server": "192.0.2.1", "server_port": float64(443), "uuid": "bf000d23-0000-4000-8000-000000000000",
	}}
}

func admitBackend(t *testing.T, check configChecker, active string, auto bool) (*Backend, *recordLog) {
	t.Helper()
	log := &recordLog{}
	b := &Backend{config: configfile.New(filepath.Join(t.TempDir(), "config.json")), log: log, check: check}
	err := b.Configure(engine.Configuration{Profiles: []profile.Profile{brokenAwg(), finka()}, ActiveProfile: active, AutoSelect: auto, Routing: routing.DefaultRoutingState()})
	if err != nil {
		t.Fatal(err)
	}
	return b, log
}

func written(t *testing.T, b *Backend) (endpoints, outbounds []string) {
	t.Helper()
	cfg, err := b.config.Read()
	if err != nil {
		t.Fatal(err)
	}
	for key, out := range map[string]*[]string{"endpoints": &endpoints, "outbounds": &outbounds} {
		blocks, _ := cfg[key].([]interface{})
		for _, raw := range blocks {
			tag, _ := raw.(map[string]interface{})["tag"].(string)
			*out = append(*out, tag)
		}
	}
	return endpoints, outbounds
}

func contains(tags []string, tag string) bool {
	for _, t := range tags {
		if t == tag {
			return true
		}
	}
	return false
}

// A check that answers as the core did on tester-002's machine.
func refusesEndpoint0(_ context.Context, path string) error {
	return &rejectedConfig{detail: `endpoints[0].unknown: json: unknown field "unknown"`}
}

// One profile the core refuses must not take the rest down: the other profile
// starts, the refused one is left out of the config, and selecting it later
// says why.
func TestRefusedProfileIsLeftOutAndTheRestStart(t *testing.T) {
	checks := 0
	check := func(ctx context.Context, path string) error {
		checks++
		if endpoints, _ := writtenAt(t, path); contains(endpoints, "p-amnezia") {
			return refusesEndpoint0(ctx, path)
		}
		return nil
	}
	b, _ := admitBackend(t, check, "finka", false)
	if err := b.admit(context.Background()); err != nil {
		t.Fatal(err)
	}
	endpoints, outbounds := written(t, b)
	if contains(endpoints, "p-amnezia") || !contains(outbounds, "p-finka") || checks != 2 {
		t.Fatalf("endpoints %v outbounds %v after %d checks", endpoints, outbounds, checks)
	}
	err := b.SelectProfile("amnezia")
	if err == nil || !strings.Contains(err.Error(), "«Amnezia Premium»") || !strings.Contains(err.Error(), `unknown field "unknown"`) {
		t.Fatalf("select of the refused profile: %v", err)
	}
}

// The user asked for this server: starting another one instead would hide why
// it does not work.
func TestRefusedActiveProfileFailsTheStartWithTheCoresReason(t *testing.T) {
	b, _ := admitBackend(t, refusesEndpoint0, "amnezia", false)
	err := b.admit(context.Background())
	if err == nil || err.Error() != `профиль «Amnezia Premium» не принят ядром: endpoints[0].unknown: json: unknown field "unknown"` {
		t.Fatalf("start: %v", err)
	}
}

// Automatic selection has no server the user asked for: the rest start.
func TestRefusedActiveProfileIsLeftOutUnderAutoSelect(t *testing.T) {
	check := func(ctx context.Context, path string) error {
		if endpoints, _ := writtenAt(t, path); contains(endpoints, "p-amnezia") {
			return refusesEndpoint0(ctx, path)
		}
		return nil
	}
	b, _ := admitBackend(t, check, "amnezia", true)
	if err := b.admit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b.active != "p-finka" {
		t.Fatalf("active %q", b.active)
	}
}

// A refusal that names no profile is the core's own words, not a guess.
func TestRefusalOfNoProfileIsReported(t *testing.T) {
	check := func(context.Context, string) error {
		return &rejectedConfig{detail: `dns.servers[0].x: json: unknown field "x"`}
	}
	b, _ := admitBackend(t, check, "finka", false)
	err := b.admit(context.Background())
	if err == nil || err.Error() != `ядро не приняло конфиг: dns.servers[0].x: json: unknown field "x"` {
		t.Fatalf("start: %v", err)
	}
	if endpoints, _ := written(t, b); !contains(endpoints, "p-amnezia") {
		t.Fatal("a profile was dropped over a refusal that does not name it")
	}
}

// The real core, on the config the client writes: the refused profile goes, the
// other one is what the core then takes.
func TestRealCoreRefusalLeavesOnlyTheBrokenProfileOut(t *testing.T) {
	core := realCore(t)
	b, log := admitBackend(t, checkWithCore(core), "finka", false)
	if err := b.admit(context.Background()); err != nil {
		t.Fatalf("%v\n%v", err, log.lines)
	}
	endpoints, outbounds := written(t, b)
	if contains(endpoints, "p-amnezia") || !contains(outbounds, "p-finka") {
		t.Fatalf("endpoints %v outbounds %v", endpoints, outbounds)
	}
	if err := b.check(context.Background(), b.config.Path()); err != nil {
		t.Fatalf("the core still refuses: %v", err)
	}
	if err := b.rejected["amnezia"]; err == nil || !strings.Contains(err.Error(), `unknown field "unknown"`) || strings.Contains(err.Error(), b.config.Path()) {
		t.Fatalf("reason kept for the refused profile: %v", err)
	}
}

func writtenAt(t *testing.T, path string) (endpoints, outbounds []string) {
	t.Helper()
	return written(t, &Backend{config: configfile.New(path)})
}

func TestCoreReasonDropsColourLevelAndPath(t *testing.T) {
	out := "\x1b[31mFATAL\x1b[0m[0000] decode config at C:\\Program Files\\hikkari\\core\\config.json: endpoints[0].peers[0].preshared_key: json: unknown field \"preshared_key\"\n"
	got := vpn.CoreReason(out, `C:\Program Files\hikkari\core\config.json`)
	if got != `endpoints[0].peers[0].preshared_key: json: unknown field "preshared_key"` {
		t.Fatalf("%q", got)
	}
}
