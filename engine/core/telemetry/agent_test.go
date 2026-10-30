package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type sink struct {
	mu      sync.Mutex
	batches []Batch
	fail    error
}

func (s *sink) send(_ context.Context, _ string, body []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		return s.fail
	}
	var b Batch
	if err := json.Unmarshal(body, &b); err != nil {
		return err
	}
	s.batches = append(s.batches, b)
	return nil
}

func newAgent(t *testing.T, dir string, s *sink, now *time.Time) *Agent {
	t.Helper()
	a := New(Options{
		StatePath: filepath.Join(dir, "telemetry.json"),
		QueuePath: filepath.Join(dir, "telemetry-queue.json"),
		URL:       "https://hub.test/v1/events",
		App:       func() App { return App{Version: "1.0.4", Channel: "stable", OS: "macos", Arch: "arm64"} },
		Send:      s.send,
		Now:       func() time.Time { return *now },
	})
	a.Load()
	return a
}

func TestCleanInstallSendsInstallThenLaunch(t *testing.T) {
	now := time.Unix(1761900000, 0)
	s := &sink{}
	a := newAgent(t, t.TempDir(), s, &now)
	a.Start(true)
	if err := a.sendAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(s.batches) != 1 {
		t.Fatalf("batches = %d", len(s.batches))
	}
	b := s.batches[0]
	if !strings.HasPrefix(b.Install, "t-") || len(b.Install) != 34 {
		t.Fatalf("install = %q", b.Install)
	}
	if len(b.Events) != 2 || b.Events[0].Name != "install" || b.Events[1].Name != "launch" {
		t.Fatalf("events = %+v", b.Events)
	}
	if b.App.Version != "1.0.4" {
		t.Fatalf("app = %+v", b.App)
	}
}

func TestSecondLaunchIsNotAnInstallAndKeepsTheID(t *testing.T) {
	now := time.Unix(1761900000, 0)
	dir := t.TempDir()
	s := &sink{}
	a := newAgent(t, dir, s, &now)
	a.Start(true)
	_ = a.sendAll(context.Background())
	first := s.batches[0].Install

	b := newAgent(t, dir, s, &now)
	b.Start(true)
	_ = b.sendAll(context.Background())
	last := s.batches[len(s.batches)-1]
	if last.Install != first {
		t.Fatalf("install ID changed across launches: %s → %s", first, last.Install)
	}
	for _, e := range last.Events {
		if e.Name == "install" {
			t.Fatal("second launch reported an install")
		}
	}
}

func TestOffSendsNothingAndForgetsEverything(t *testing.T) {
	now := time.Unix(1761900000, 0)
	dir := t.TempDir()
	s := &sink{}
	var told []bool
	a := newAgent(t, dir, s, &now)
	a.o.OnChange = func(on bool) { told = append(told, on) }
	a.Start(true)
	_ = a.Error("engine.start", "singbox", "dial 203.0.113.7:443 failed")
	if err := a.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	_ = a.Event("ui_open", map[string]any{"screen": "logs"})
	_ = a.Error("engine.start", "singbox", "again")
	if err := a.sendAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(s.batches) != 0 {
		t.Fatalf("sent %d batches while off", len(s.batches))
	}
	if a.LastBatch() != nil {
		t.Fatal("a batch is still shown while off")
	}
	if len(told) != 1 || told[0] {
		t.Fatalf("OnChange calls = %v", told)
	}

	// Back on: a new, unrelated ID, and a reload keeps the explicit choice.
	_ = a.SetEnabled(true)
	_ = a.Event("ui_open", map[string]any{"screen": "logs"})
	_ = a.sendAll(context.Background())
	re := newAgent(t, dir, s, &now)
	if st := re.Settings(); !st.Enabled || !st.Explicit {
		t.Fatalf("settings after reload = %+v", st)
	}
}

func TestExplicitChoiceSurvivesAFlippedDefault(t *testing.T) {
	now := time.Unix(1761900000, 0)
	dir := t.TempDir()
	defer func(d string) { Default = d }(Default)

	Default = "on"
	a := newAgent(t, dir, &sink{}, &now)
	_ = a.SetEnabled(false)

	Default = "on" // a later build keeps the default on: the user stays off
	if newAgent(t, dir, &sink{}, &now).Enabled() {
		t.Fatal("explicit off was overridden by the default")
	}

	other := t.TempDir()
	Default = "off" // a later build flips it: users who never chose follow
	if newAgent(t, other, &sink{}, &now).Enabled() {
		t.Fatal("no choice + default off should be off")
	}
	_ = newAgent(t, other, &sink{}, &now).SetEnabled(true)
	if !newAgent(t, other, &sink{}, &now).Enabled() {
		t.Fatal("explicit on was overridden by default off")
	}
}

func TestContractRefusesWhatItDoesNotList(t *testing.T) {
	now := time.Unix(1761900000, 0)
	a := newAgent(t, t.TempDir(), &sink{}, &now)
	bad := []struct {
		name   string
		fields map[string]any
	}{
		{"page_view", nil},
		{"ui_open", map[string]any{"screen": "secret-admin"}},
		{"connect", map[string]any{"server": "203.0.113.7"}},
		{"connect", map[string]any{"error": "made.up"}},
		{"update", map[string]any{"to_version": "latest"}},
		{"connect", map[string]any{"secs": -1}},
	}
	for _, c := range bad {
		if err := a.Event(c.name, c.fields); err == nil {
			t.Errorf("%s %v was accepted", c.name, c.fields)
		}
	}
	if err := a.Error("made.up", "engine", "x"); err == nil {
		t.Error("unknown error code accepted")
	}
}

func TestCountsLeaveOnlyAsBuckets(t *testing.T) {
	cases := []struct {
		n      int
		bounds []int
		want   string
	}{
		{0, []int{2, 6}, "0"}, {1, []int{2, 6}, "1"}, {4, []int{2, 6}, "2-5"}, {40, []int{2, 6}, "6+"},
		{10, []int{2, 11}, "2-10"}, {11, []int{2, 11}, "11+"}, {7, []int{2}, "2+"},
	}
	for _, c := range cases {
		if got := bucket(c.n, c.bounds); got != c.want {
			t.Errorf("bucket(%d, %v) = %s, want %s", c.n, c.bounds, got, c.want)
		}
	}
	got, err := normalize("daily_active", map[string]any{"connected_minutes": 137.0, "profiles": 9.0})
	if err != nil {
		t.Fatal(err)
	}
	if got["connected_minutes"] != 130 || got["profiles"] != "6+" {
		t.Fatalf("normalized = %v", got)
	}
}

func TestErrorsAreScrubbedAndFolded(t *testing.T) {
	now := time.Unix(1761900000, 0)
	s := &sink{}
	a := newAgent(t, t.TempDir(), s, &now)
	_ = a.Error("engine.start", "singbox", "dial 203.0.113.7:443 attempt 1 failed")
	_ = a.Error("engine.start", "singbox", "dial 198.51.100.2:8443 attempt 2 failed")
	_ = a.Error("engine.start", "singbox", strings.Repeat("я", 1000))
	_ = a.sendAll(context.Background())
	errs := s.batches[0].Errors
	if len(errs) != 2 || errs[0].Count != 2 {
		t.Fatalf("errors = %+v", errs)
	}
	if strings.Contains(errs[0].Msg, "203.0.113.7") {
		t.Fatalf("address survived: %s", errs[0].Msg)
	}
	if n := len([]rune(errs[1].Msg)); n != MsgMaxRunes {
		t.Fatalf("msg length = %d", n)
	}
}

func TestFailedSendKeepsTheQueueAndRejectionDropsIt(t *testing.T) {
	now := time.Unix(1761900000, 0)
	s := &sink{fail: errors.New("network down")}
	a := newAgent(t, t.TempDir(), s, &now)
	_ = a.Event("ui_open", map[string]any{"screen": "logs"})
	if err := a.sendAll(context.Background()); err == nil {
		t.Fatal("a network failure was reported as success")
	}
	if a.LastBatch() == nil {
		t.Fatal("the queue was lost on a network failure")
	}
	s.fail = errRejected
	if err := a.sendAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	left := len(a.q.Events)
	a.mu.Unlock()
	if left != 0 {
		t.Fatalf("rejected batch still queued: %d", left)
	}
}

func TestQueueStaysBoundedOffline(t *testing.T) {
	now := time.Unix(1761900000, 0)
	a := newAgent(t, t.TempDir(), &sink{}, &now)
	a.o.URL = "" // no endpoint: a week offline
	for i := 0; i < 30000; i++ {
		now = now.Add(20 * time.Second)
		_ = a.Event("connect", map[string]any{"engine": "xray", "proto": "vless-xhttp", "ok": false, "secs": i, "error": "engine.start"})
	}
	a.mu.Lock()
	raw, _ := json.Marshal(a.q)
	oldest := a.q.Events[0].TS
	a.mu.Unlock()
	if len(raw) > QueueMaxBytes {
		t.Fatalf("queue is %d bytes", len(raw))
	}
	if now.Unix()-oldest > int64(QueueMaxAge/time.Second) {
		t.Fatal("queue holds events older than a week")
	}
}

func TestBatchesRespectTheLimits(t *testing.T) {
	now := time.Unix(1761900000, 0)
	s := &sink{}
	a := newAgent(t, t.TempDir(), s, &now)
	for i := 0; i < 450; i++ {
		_ = a.Event("ui_open", map[string]any{"screen": "routing"})
	}
	if err := a.sendAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, b := range s.batches {
		if n := len(b.Events) + len(b.Errors); n > BatchMaxItems {
			t.Fatalf("batch of %d items", n)
		}
		raw, _ := json.Marshal(b)
		if len(raw) > BatchMaxBytes {
			t.Fatalf("batch of %d bytes", len(raw))
		}
		total += len(b.Events)
	}
	if total != 450 {
		t.Fatalf("sent %d of 450", total)
	}
}

func TestFirstConnectIsSentOnce(t *testing.T) {
	now := time.Unix(1761900000, 0)
	s := &sink{}
	a := newAgent(t, t.TempDir(), s, &now)
	a.Start(true)
	now = now.Add(90 * time.Second)
	_ = a.FirstConnect("singbox", "vless-reality")
	_ = a.FirstConnect("singbox", "vless-reality")
	_ = a.sendAll(context.Background())
	n := 0
	for _, e := range s.batches[0].Events {
		if e.Name == "first_connect_ok" {
			n++
			if e.Fields["secs_since_install"].(float64) != 90 {
				t.Fatalf("fields = %v", e.Fields)
			}
		}
	}
	if n != 1 {
		t.Fatalf("first_connect_ok sent %d times", n)
	}
}

func TestPostClassifiesHubAnswers(t *testing.T) {
	codes := map[int]bool{200: false, 204: false, 400: true, 413: true, 429: false, 500: false, 503: false}
	for code, rejected := range codes {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Content-Type") != "application/json" || r.Method != http.MethodPost {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.WriteHeader(code)
		}))
		err := post(context.Background(), srv.URL, []byte(`{}`))
		srv.Close()
		ok := code < 300
		if (err == nil) != ok || errors.Is(err, errRejected) != rejected {
			t.Errorf("HTTP %d → %v", code, err)
		}
	}
}

func TestCloseWritesThePendingQueue(t *testing.T) {
	now := time.Unix(1761900000, 0)
	dir := t.TempDir()
	a := newAgent(t, dir, &sink{}, &now)
	_ = a.Event("ui_open", map[string]any{"screen": "support"})
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	re := newAgent(t, dir, &sink{}, &now)
	re.mu.Lock()
	n := len(re.q.Events)
	re.mu.Unlock()
	if n != 1 {
		t.Fatalf("reloaded queue has %d events", n)
	}
}
