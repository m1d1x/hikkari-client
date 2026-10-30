package presets

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"NekkoVPN/core/routing"
)

func testCatalog(version int64, domain string) []byte {
	c, _ := Parse(bundled)
	c.Version = version
	c.Services[0].Domains = []string{domain}
	data, _ := json.Marshal(c)
	return data
}

func newManager(t *testing.T, path string, options Options) *Manager {
	t.Helper()
	m, err := New(path, options)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestGroupsAreIndependentOfManualRules(t *testing.T) {
	c, err := Parse(bundled)
	if err != nil {
		t.Fatal(err)
	}
	// Isolate manual-rule ownership from the shipped default selections.
	for i := range c.Services {
		c.Services[i].DefaultModes = nil
	}
	manual := routing.DefaultRoutingState()
	manual.Bypass.Domains = []string{"steampowered.com", "mine.example"}
	choices := Choices{routing.ModeBypass: {"steam": true}}
	effective, err := Effective(manual, c, choices)
	if err != nil {
		t.Fatal(err)
	}
	if len(effective.Bypass.Domains) <= len(manual.Bypass.Domains) || len(manual.Bypass.Domains) != 2 {
		t.Fatal("manual policy changed or group missing")
	}
	choices[routing.ModeBypass]["steam"] = false
	if got, err := Effective(manual, c, choices); err != nil || !reflect.DeepEqual(got, manual) {
		t.Fatal("disabling group removed manual data", got)
	}
	if Selected(c, choices, routing.ModeProxy, "steam") {
		t.Fatal("selection leaked between modes")
	}
}

func TestCatalogRejectsInvalidDocuments(t *testing.T) {
	for _, data := range []string{
		`{"schemaVersion":9,"version":2,"services":[]}`,
		`{"schemaVersion":1,"version":0,"services":[]}`,
		`{"schemaVersion":1,"version":2,"services":[],"execute":"bad"}`,
		string(testCatalog(3, "https://wrong.example/path")),
		string(testCatalog(3, "bad domain.example")),
		string(bundled) + ` {}`,
		strings.Repeat("x", MaxCatalogBytes+1),
	} {
		if _, err := Parse([]byte(data)); err == nil {
			t.Fatalf("accepted invalid catalog: %.80s", data)
		}
	}
}

func TestRefreshPersistenceOptOutAndLastGoodFallback(t *testing.T) {
	var mu sync.Mutex
	body := testCatalog(3, "new.example")
	status := 200
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests++
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "presets.json")
	m := newManager(t, path, Options{URL: server.URL})
	if !m.Snapshot().SyncEnabled || m.Snapshot().Source != "bundled" {
		t.Fatal("wrong defaults")
	}
	if err := m.Select(routing.ModeProxy, "steam", true); err != nil {
		t.Fatal(err)
	}
	if err := m.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	status = 503
	mu.Unlock()
	if err := m.Refresh(context.Background()); err == nil {
		t.Fatal("missing HTTP failure")
	}
	if got := m.Snapshot(); got.Catalog.Version != 3 || got.Error == "" {
		t.Fatal("last-good cache lost")
	}
	if err := m.SetSync(false); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	before := requests
	mu.Unlock()
	_ = m.Refresh(context.Background())
	mu.Lock()
	after := requests
	mu.Unlock()
	if before != after {
		t.Fatal("opt-out still fetches")
	}
	restored := newManager(t, path, Options{URL: server.URL}).Snapshot()
	if restored.SyncEnabled || restored.Catalog.Version != 3 || !Selected(restored.Catalog, restored.Choices, routing.ModeProxy, "steam") {
		t.Fatal("choices/version not restored")
	}
}

func TestVersionValidationAndETag(t *testing.T) {
	var body = testCatalog(3, "v2.example")
	var notModified bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if notModified {
			if r.Header.Get("If-None-Match") != `"v2"` {
				t.Error("ETag not sent")
			}
			w.WriteHeader(304)
			return
		}
		w.Header().Set("ETag", `"v2"`)
		_, _ = w.Write(body)
	}))
	defer server.Close()
	m := newManager(t, "", Options{URL: server.URL})
	if err := m.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	notModified = true
	if err := m.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	notModified = false
	for _, invalid := range [][]byte{testCatalog(2, "old.example"), testCatalog(3, "changed.example"), []byte(`{`)} {
		body = invalid
		if err := m.Refresh(context.Background()); err == nil {
			t.Fatal("bad update accepted")
		}
		if m.Snapshot().Catalog.Version != 3 || m.Snapshot().Catalog.Services[0].Domains[0] != "v2.example" {
			t.Fatal("working data overwritten")
		}
	}
}

func TestFailedApplicationRestoresDiskAndState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(testCatalog(3, "v2.example")) }))
	defer server.Close()
	var applied []int64
	path := filepath.Join(t.TempDir(), "state.json")
	m := newManager(t, path, Options{URL: server.URL, Apply: func(s State) error {
		applied = append(applied, s.Catalog.Version)
		if s.Catalog.Version == 3 {
			return fmt.Errorf("backend rejected update")
		}
		return nil
	}})
	if err := m.Refresh(context.Background()); err == nil {
		t.Fatal("application error lost")
	}
	if !reflect.DeepEqual(applied, []int64{3, 2}) {
		t.Fatal("rollback not applied", applied)
	}
	if got := newManager(t, path, Options{}).Snapshot(); got.Catalog.Version != 2 {
		t.Fatal("bad state persisted")
	}
	if m.Snapshot().Catalog.Version != 2 {
		t.Fatal("bad state published")
	}
}

func TestOptOutWinsAgainstInFlightResponse(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		_, _ = w.Write(testCatalog(3, "new.example"))
	}))
	defer server.Close()
	m := newManager(t, "", Options{URL: server.URL})
	done := make(chan error, 1)
	go func() { done <- m.Refresh(context.Background()) }()
	<-entered
	if err := m.SetSync(false); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := m.Snapshot(); got.SyncEnabled || got.Catalog.Version != 2 {
		t.Fatal("in-flight update ignored opt-out")
	}
}

func TestSSEInvalidationFetchesAndAppliesCatalog(t *testing.T) {
	var version atomic.Int64
	version.Store(3)
	events := make(chan struct{}, 4)
	connected := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/events" {
			_, _ = w.Write(testCatalog(version.Load(), "service.example"))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, ": connected\n\n")
		w.(http.Flusher).Flush()
		select {
		case connected <- struct{}{}:
		default:
		}
		for {
			select {
			case <-r.Context().Done():
				return
			case <-events:
				_, _ = fmt.Fprint(w, "event: catalog\ndata: changed\n\n")
				w.(http.Flusher).Flush()
			}
		}
	}))
	defer server.Close()
	m := newManager(t, "", Options{URL: server.URL, EventsURL: server.URL + "/events"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	select {
	case <-connected:
	case <-time.After(3 * time.Second):
		t.Fatal("no SSE connection")
	}
	waitVersion(t, m, 3)
	version.Store(4)
	events <- struct{}{}
	waitVersion(t, m, 4)
	if err := m.SetSync(false); err != nil {
		t.Fatal(err)
	}
	version.Store(5)
	events <- struct{}{}
	if err := m.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot().Catalog.Version != 4 {
		t.Fatal("SSE bypassed opt-out")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not stop")
	}
}

func waitVersion(t *testing.T, m *Manager, v int64) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if m.Snapshot().Catalog.Version == v {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("version %d not received: %+v", v, m.Snapshot())
}

func TestCorruptCacheFallsBackAndInsecureEndpointRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	_ = os.WriteFile(path, []byte("broken"), 0600)
	if s := newManager(t, path, Options{}).Snapshot(); s.Source != "bundled" || s.Error == "" {
		t.Fatal("corruption not surfaced")
	}
	for _, endpoint := range []string{"http://example.com/catalog", "https://user:pass@example.com/catalog", "file:///tmp/catalog"} {
		if _, err := New("", Options{URL: endpoint}); err == nil {
			t.Fatal("insecure endpoint accepted")
		}
	}
}

func TestBundledDefaultsAndUpgradePreserveExplicitChoices(t *testing.T) {
	fresh := newManager(t, "", Options{}).Snapshot()
	for _, mode := range []routing.RoutingMode{routing.ModeBypass, routing.ModeProxy} {
		for _, service := range fresh.Catalog.Services {
			if !Selected(fresh.Catalog, fresh.Choices, mode, service.ID) {
				t.Fatalf("%s not enabled by default in %s", service.ID, mode)
			}
		}
	}
	legacy := fresh.State
	legacy.Catalog.Version = 1
	for i := range legacy.Catalog.Services {
		legacy.Catalog.Services[i].DefaultModes = nil
	}
	legacy.SyncEnabled = false
	legacy.Choices = Choices{routing.ModeBypass: {"steam": false}}
	path := filepath.Join(t.TempDir(), "presets.json")
	data, _ := json.Marshal(legacy)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	upgraded := newManager(t, path, Options{}).Snapshot()
	if upgraded.SyncEnabled || Selected(upgraded.Catalog, upgraded.Choices, routing.ModeBypass, "steam") {
		t.Fatal("upgrade overwrote explicit opt-outs")
	}
	if !Selected(upgraded.Catalog, upgraded.Choices, routing.ModeBypass, "discord") {
		t.Fatal("bundled defaults were not upgraded")
	}
	if !Selected(upgraded.Catalog, upgraded.Choices, routing.ModeProxy, "steam") {
		t.Fatal("bypass opt-out leaked to proxy")
	}
	legacy.Source = "cache"
	data, _ = json.Marshal(legacy)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	cached := newManager(t, path, Options{}).Snapshot()
	if cached.Catalog.Version != 1 {
		t.Fatal("upgrade overwrote remote cache")
	}
}

func TestCatalogFoldersReachTheirPlatformOnly(t *testing.T) {
	data := `{"schemaVersion":1,"version":2,"services":[{"id":"games","name":"Games","icon":"","domains":[],"ips":[],"processes":{},"folders":{"windows":["steamapps/common/"],"darwin":["Discord.app/"]},"defaultModes":["bypass"]}]}`
	c, err := Parse([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	doc := Document(routing.DefaultRoutingState(), c, Choices{}, "windows")
	got, err := doc.Resolve("windows")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Bypass.Folders, []string{"steamapps/common/"}) {
		t.Fatalf("windows folders = %v", got.Bypass.Folders)
	}
	for _, bad := range []string{
		`{"schemaVersion":1,"version":2,"services":[{"id":"x","name":"X","icon":"","domains":[],"ips":[],"processes":{},"folders":{"windows":["steam"]},"defaultModes":[]}]}`,
		`{"schemaVersion":1,"version":2,"services":[{"id":"x","name":"X","icon":"","domains":[],"ips":[],"processes":{},"folders":{"android":["a/"]},"defaultModes":[]}]}`,
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Fatalf("accepted invalid folders: %s", bad)
		}
	}
}
