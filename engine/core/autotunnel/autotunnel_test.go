package autotunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 2, 5, 36, 0, 0, time.UTC)

func silent(id, host string, age time.Duration) Conn {
	return Conn{ID: id, Host: host, Network: "tcp", Port: "443", Upload: 2200, Direct: true, Start: t0.Add(-age)}
}

func TestDetectorProvesAHostFromTwoSilentConnections(t *testing.T) {
	d := NewDetector()
	if got := d.Observe(t0, []Conn{silent("a", "play.google.com", time.Minute)}); len(got) != 0 {
		t.Fatalf("one connection proved %v", got)
	}
	got := d.Observe(t0.Add(5*time.Second), []Conn{silent("a", "play.google.com", time.Minute), silent("b", "play.google.com", time.Minute)})
	if !reflect.DeepEqual(got, []string{"play.google.com"}) {
		t.Fatalf("got %v", got)
	}
}

func TestDetectorCountsRetriesSeenAtDifferentTimes(t *testing.T) {
	d := NewDetector()
	d.Observe(t0, []Conn{silent("a", "x.example", time.Minute)})
	// The first connection is gone: browsers give up on one and open another.
	got := d.Observe(t0.Add(30*time.Second), []Conn{silent("b", "x.example", time.Minute)})
	if !reflect.DeepEqual(got, []string{"x.example"}) {
		t.Fatalf("got %v", got)
	}
}

func TestDetectorLeavesWhatIsNotProven(t *testing.T) {
	answered := silent("c", "ok.example", time.Minute)
	answered.Download = 5000
	young := []Conn{silent("a", "young.example", time.Second), silent("b", "young.example", time.Second)}
	notTLS := silent("a", "port.example", time.Minute)
	notTLS.Port = "8080"
	notTLS2 := silent("b", "port.example", time.Minute)
	notTLS2.Port = "8080"
	tunnelled := silent("a", "tunnel.example", time.Minute)
	tunnelled.Direct = false
	tunnelled2 := silent("b", "tunnel.example", time.Minute)
	tunnelled2.Direct = false
	bare := []Conn{silent("a", "203.0.113.9", time.Minute), silent("b", "203.0.113.9", time.Minute), silent("c", "", time.Minute), silent("d", "", time.Minute)}
	noHello := silent("a", "idle.example", time.Minute)
	noHello.Upload = 0
	noHello2 := silent("b", "idle.example", time.Minute)
	noHello2.Upload = 0
	cases := map[string][]Conn{
		"one answered":   {silent("a", "ok.example", time.Minute), silent("b", "ok.example", time.Minute), answered},
		"still young":    young,
		"another port":   {notTLS, notTLS2},
		"through tunnel": {tunnelled, tunnelled2},
		"no name":        bare,
		"never asked":    {noHello, noHello2},
	}
	for name, conns := range cases {
		if got := NewDetector().Observe(t0, conns); len(got) != 0 {
			t.Errorf("%s: proved %v", name, got)
		}
	}
}

func TestDetectorForgetsOldEvidence(t *testing.T) {
	d := NewDetector()
	d.Observe(t0, []Conn{silent("a", "x.example", time.Minute)})
	if got := d.Observe(t0.Add(silentWindow+time.Second), []Conn{silent("b", "x.example", time.Minute)}); len(got) != 0 {
		t.Fatalf("stale evidence counted: %v", got)
	}
}

func TestAnAnswerClearsTheEvidence(t *testing.T) {
	d := NewDetector()
	d.Observe(t0, []Conn{silent("a", "x.example", time.Minute)})
	ok := silent("b", "x.example", time.Minute)
	ok.Download = 100
	d.Observe(t0.Add(time.Second), []Conn{ok})
	if got := d.Observe(t0.Add(2*time.Second), []Conn{silent("c", "x.example", time.Minute)}); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func readRuleSet(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var rs struct {
		Version int `json:"version"`
		Rules   []struct {
			Domain []string `json:"domain"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(raw, &rs); err != nil {
		t.Fatal(err)
	}
	if rs.Version != 3 {
		t.Fatalf("version %d", rs.Version)
	}
	if len(rs.Rules) == 0 {
		return nil
	}
	return rs.Rules[0].Domain
}

func TestListWritesTheRuleSetAndSurvivesARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auto-tunnel.json")
	l := OpenList(path, nil)
	if err := l.Add(t0, []string{"b.example", "a.example"}); err != nil {
		t.Fatal(err)
	}
	if got := readRuleSet(t, path); !reflect.DeepEqual(got, []string{"a.example", "b.example"}) {
		t.Fatalf("rule set: %v", got)
	}
	if got := OpenList(path, nil).Hosts(); !reflect.DeepEqual(got, []string{"a.example", "b.example"}) {
		t.Fatalf("after restart: %v", got)
	}
}

func TestListExpiresAndResets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auto-tunnel.json")
	l := OpenList(path, nil)
	if err := l.Add(t0, []string{"old.example"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Add(t0.Add(entryTTL/2), []string{"new.example"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Expire(t0.Add(entryTTL + time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := readRuleSet(t, path); !reflect.DeepEqual(got, []string{"new.example"}) {
		t.Fatalf("after expiry: %v", got)
	}
	if err := l.Reset(); err != nil {
		t.Fatal(err)
	}
	if got := readRuleSet(t, path); got != nil {
		t.Fatalf("after reset: %v", got)
	}
}

func TestListIsBounded(t *testing.T) {
	l := OpenList(filepath.Join(t.TempDir(), "auto-tunnel.json"), nil)
	for i := 0; i < maxHosts+5; i++ {
		if err := l.Add(t0.Add(time.Duration(i)*time.Second), []string{"h" + strings.Repeat("x", i) + ".example"}); err != nil {
			t.Fatal(err)
		}
	}
	hosts := l.Hosts()
	if len(hosts) != maxHosts {
		t.Fatalf("%d hosts", len(hosts))
	}
	if strings.Contains(strings.Join(hosts, " "), "h.example") {
		t.Fatal("kept the oldest entry")
	}
}

func TestEnsureRuleSetKeepsWhatIsThere(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auto-tunnel.json")
	if err := EnsureRuleSet(path); err != nil {
		t.Fatal(err)
	}
	if got := readRuleSet(t, path); got != nil {
		t.Fatalf("new file: %v", got)
	}
	if err := OpenList(path, nil).Add(t0, []string{"a.example"}); err != nil {
		t.Fatal(err)
	}
	if err := EnsureRuleSet(path); err != nil {
		t.Fatal(err)
	}
	if got := readRuleSet(t, path); len(got) != 1 {
		t.Fatalf("overwritten: %v", got)
	}
}

func TestRunStopsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		Run(ctx, OpenList(filepath.Join(t.TempDir(), "a.json"), nil), func() ([]Conn, error) { return nil, nil }, func(string, ...interface{}) {})
	}()
	cancel()
	wg.Wait()
}

func TestListPublishesEveryChange(t *testing.T) {
	var mu sync.Mutex
	var published [][]string
	l := OpenList(filepath.Join(t.TempDir(), "auto-tunnel.json"), nil)
	l.SetPublish(func(hosts []string) error {
		mu.Lock()
		defer mu.Unlock()
		published = append(published, hosts)
		return nil
	})
	if err := l.Add(t0, []string{"b.example", "a.example"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Add(t0.Add(time.Second), []string{"c.example"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Expire(t0.Add(entryTTL + time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := l.Reset(); err != nil { // nothing left: no save, no publish
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	want := [][]string{{"a.example", "b.example"}, {"a.example", "b.example", "c.example"}, {}}
	if !reflect.DeepEqual(published, want) {
		t.Fatalf("published %v, want %v", published, want)
	}
}

func TestPublishPushesTheCurrentListWithoutSaving(t *testing.T) {
	l := OpenList(filepath.Join(t.TempDir(), "auto-tunnel.json"), nil)
	if err := l.Add(t0, []string{"a.example"}); err != nil {
		t.Fatal(err)
	}
	pushed := 0
	l.SetPublish(func(hosts []string) error {
		pushed++
		if !reflect.DeepEqual(hosts, []string{"a.example"}) {
			t.Errorf("push: %v", hosts)
		}
		return nil
	})
	if err := l.Publish(); err != nil {
		t.Fatal(err)
	}
	if err := l.Publish(); err != nil {
		t.Fatal(err)
	}
	if pushed != 2 {
		t.Fatalf("pushed %d times", pushed)
	}
}

func TestPublishWithoutAWireIsANoOp(t *testing.T) {
	l := OpenList(filepath.Join(t.TempDir(), "auto-tunnel.json"), nil)
	if err := l.Publish(); err != nil {
		t.Fatalf("no hook, no error: %v", err)
	}
}

func TestValidateHosts(t *testing.T) {
	ok := [][]string{
		nil,
		{"a.example"},
		{"a.example", "b.example"},
	}
	for _, hosts := range ok {
		if err := ValidateHosts(hosts); err != nil {
			t.Errorf("ValidateHosts(%v): %v", hosts, err)
		}
	}
	bad := [][]string{
		{"localhost"},          // no dot: not routable by name
		{"192.0.2.1"},          // an address, not a domain
		{"a b.example"},        // whitespace
		{"a.example/b"},        // path separator
		{"a.example", ""},      // empty
		{strings.Repeat("x", 254) + ".example"}, // longer than a domain may be
	}
	for _, hosts := range bad {
		if err := ValidateHosts(hosts); err == nil {
			t.Errorf("ValidateHosts(%v) accepted", hosts)
		}
	}
	many := make([]string, maxHosts+1)
	for i := range many {
		many[i] = fmt.Sprintf("h%d.example", i)
	}
	if err := ValidateHosts(many); err == nil {
		t.Error("over the bound accepted")
	}
	dup := []string{"a.example", "a.example"}
	if err := ValidateHosts(dup); err == nil {
		t.Error("duplicate accepted")
	}
}

func TestRunPublishesAtStartAndOnAHeartbeat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.json")
	l := OpenList(path, nil)
	if err := l.Add(t0, []string{"a.example"}); err != nil {
		t.Fatal(err)
	}
	pushes := make(chan []string, 4)
	l.SetPublish(func(hosts []string) error {
		pushes <- hosts
		return nil
	})
	prev := resyncInterval
	resyncInterval = 20 * time.Millisecond
	defer func() { resyncInterval = prev }()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		Run(ctx, l, func() ([]Conn, error) { return nil, nil }, func(string, ...interface{}) {})
	}()
	select {
	case hosts := <-pushes:
		if !reflect.DeepEqual(hosts, []string{"a.example"}) {
			t.Fatalf("initial push: %v", hosts)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no push at start")
	}
	select {
	case <-pushes: // heartbeat re-publishes without any change
	case <-time.After(2 * time.Second):
		t.Fatal("no heartbeat push")
	}
	cancel()
	<-done
}

func TestWriteRuleSetWritesInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auto-tunnel.json")
	if err := WriteRuleSet(path, []string{"a.example"}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// An in-place rewrite keeps the file (same inode) alive for the core's
	// watcher; a temp+rename replace would be a new file.
	if err := WriteRuleSet(path, []string{"b.example"}); err != nil {
		t.Fatal(err)
	}
	fi2, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(fi, fi2) {
		t.Fatal("the rule set was replaced, not rewritten in place")
	}
	if got := readRuleSet(t, path); !reflect.DeepEqual(got, []string{"b.example"}) {
		t.Fatalf("content: %v", got)
	}
}

func TestEnsureRuleSetLeavesAStaleListAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auto-tunnel.json")
	if err := WriteRuleSet(path, []string{"a.example"}); err != nil {
		t.Fatal(err)
	}
	if err := EnsureRuleSet(path); err != nil {
		t.Fatal(err)
	}
	if got := readRuleSet(t, path); !reflect.DeepEqual(got, []string{"a.example"}) {
		t.Fatalf("overwritten: %v", got)
	}
}
