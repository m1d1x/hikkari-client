package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"NekkoVPN/core/engine"
	"NekkoVPN/core/engine/xray"
	"NekkoVPN/core/profile"
	"NekkoVPN/core/subscription"
)

type syncEngine struct {
	alternateEngine
	configurations    int
	failConfiguration bool
	failStart         bool
}

func (f *syncEngine) Decode(s string) ([]profile.Profile, error) { return xray.Decode(s) }
func (f *syncEngine) Configure(cfg engine.Configuration) error {
	f.mu.Lock()
	f.configurations++
	fail := f.failConfiguration
	f.failConfiguration = false
	f.mu.Unlock()
	if fail {
		return errors.New("configure rejected")
	}
	return f.alternateEngine.Configure(cfg)
}
func (f *syncEngine) Start(ctx context.Context) error {
	f.mu.Lock()
	fail := f.failStart
	f.failStart = false
	f.mu.Unlock()
	if fail {
		return errors.New("start rejected")
	}
	return f.alternateEngine.Start(ctx)
}
func initSyncCore(t *testing.T) (*Core, *syncEngine) {
	t.Helper()
	t.Setenv("HIKKARI_PRESET_CATALOG_URL", "http://127.0.0.1:1")
	t.Setenv("HIKKARI_PRESET_EVENTS_URL", "http://127.0.0.1:1")
	f := &syncEngine{alternateEngine: alternateEngine{caps: engine.Capabilities{TUN: true, DomainRouting: true, IPRouting: true, AutoSelect: true, ProcessRouting: true}}}
	c := New()
	if err := c.InitWithEngine(manualRoutingDir(t), func(string, engine.Logger) (engine.Backend, error) { return f, nil }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Shutdown() })
	return c, f
}
func forceSync(t *testing.T, c *Core) {
	t.Helper()
	c.operationMu.Lock()
	pf, err := c.profiles.Read()
	if err == nil {
		for i := range pf.Subscriptions {
			pf.Subscriptions[i].NextAttempt = 0
		}
		err = c.profiles.Write(pf)
	}
	c.operationMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	c.syncSubscriptions(c.subscriptionContext(), false, false)
}
func TestAutomaticSyncMetadataRuntimeAndHiddenServers(t *testing.T) {
	c, f := initSyncCore(t)
	var phase atomic.Int32
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("X-Hwid") == "" {
			t.Error("missing installation identity")
		}
		w.Header().Set("Profile-Title", "VPN")
		w.Header().Set("Profile-Update-Interval", "6")
		if phase.Load() == 0 {
			w.Header().Set("Subscription-Userinfo", "upload=10; download=20; total=100; expire=2000000000")
			fmt.Fprint(w, "vless://key@a.test:443#A\nvless://key@b.test:443#B")
		} else {
			w.Header().Set("Subscription-Userinfo", "upload=15; download=30; total=200; expire=2100000000")
			fmt.Fprint(w, "vless://key@a.test:443#A\nvless://key@b.test:443#B")
			if phase.Load() >= 2 {
				fmt.Fprint(w, "\nvless://key@c.test:443#C")
			}
		}
	}))
	defer server.Close()
	name, err := c.AddProfileFromURI(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	pf, _ := c.profiles.Read()
	id := pf.Subscriptions[0].ID
	if pf.Subscriptions[0].URL != server.URL || pf.Profiles[0].Subscription.UpdateIntervalSeconds != 21600 {
		t.Fatal("source/header units not persisted")
	}
	if err := c.StartVPN(); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	starts, configurations := f.starts, f.configurations
	f.mu.Unlock()
	phase.Store(1)
	forceSync(t, c)
	profiles, _ := c.Profiles()
	if profiles[0].Subscription.TotalBytes != 200 || profiles[0].Subscription.DownloadBytes != 30 || profiles[0].Subscription.ExpiresAt != 2100000000 {
		t.Fatal("metadata did not update")
	}
	f.mu.Lock()
	newStarts, newConfigs := f.starts, f.configurations
	f.mu.Unlock()
	if starts != newStarts || configurations != newConfigs {
		t.Fatal("metadata-only sync interrupted VPN")
	}
	if err := c.RenameProfile(name, "My server"); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteProfile(name); err != nil {
		t.Fatal(err)
	}
	phase.Store(2)
	forceSync(t, c)
	profiles, _ = c.Profiles()
	if len(profiles) != 2 {
		t.Fatalf("hidden server returned: %+v", profiles)
	}
	for _, p := range profiles {
		if p.Name == name {
			t.Fatal("deleted server resurrected")
		}
	}
	// Reimport remains one subscription with stable IDs and hidden exclusions.
	if _, err := c.AddProfileFromURI(server.URL); err != nil {
		t.Fatal(err)
	}
	pf, _ = c.profiles.Read()
	if len(pf.Subscriptions) != 1 || pf.Subscriptions[0].ID != id || len(pf.Profiles) != 2 {
		t.Fatal("reimport duplicated subscription")
	}
	// Local reads cannot cause HTTP requests and cannot expose credential URL.
	before := requests.Load()
	raw := c.Call("Profiles", "{}")
	if strings.Contains(raw, server.URL) || requests.Load() != before {
		t.Fatal("profile projection leaked/fetched source")
	}
	stat, err := os.Stat(filepath.Join(c.baseDir, "profiles.json"))
	if err != nil {
		t.Fatal(err)
	}
	if stat.Mode().Perm()&0077 != 0 {
		t.Fatal("credential source file is not private")
	}
}
func TestSyncFailurePreservesLastGoodAndBacksOff(t *testing.T) {
	for _, mode := range []string{"http", "empty", "partial", "configure", "start"} {
		t.Run(mode, func(t *testing.T) {
			c, f := initSyncCore(t)
			var changed atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !changed.Load() {
					fmt.Fprint(w, "vless://key@a.test:443#A\nvless://key@b.test:443#B")
					return
				}
				switch mode {
				case "http":
					w.Header().Set("Retry-After", "1200")
					w.WriteHeader(429)
				case "empty":
					return
				case "partial":
					fmt.Fprint(w, "vless://key@a.test:443#A\ninvalid-record")
				default:
					fmt.Fprint(w, "vless://rotated@a.test:443#A")
				}
			}))
			defer server.Close()
			if _, err := c.AddProfileFromURI(server.URL); err != nil {
				t.Fatal(err)
			}
			if err := c.StartVPN(); err != nil {
				t.Fatal(err)
			}
			before, _ := c.profiles.Read()
			f.mu.Lock()
			f.failConfiguration = mode == "configure"
			f.failStart = mode == "start"
			f.mu.Unlock()
			changed.Store(true)
			forceSync(t, c)
			after, _ := c.profiles.Read()
			if !sameProfileRuntime(before, after) || !c.IsRunning() {
				t.Fatalf("%s damaged last working runtime", mode)
			}
			s := after.Subscriptions[0]
			if s.Failures != 1 || s.NextAttempt <= time.Now().Unix() {
				t.Fatal("failure did not back off")
			}
			if mode == "http" && s.NextAttempt < time.Now().Add(19*time.Minute).Unix() {
				t.Fatal("Retry-After ignored")
			}
			if strings.Contains(c.LogTail(), server.URL) {
				t.Fatal("logs leaked source URL")
			}
		})
	}
}
func TestSubscriptionScheduleAndStaleResponse(t *testing.T) {
	c, _ := initSyncCore(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		fmt.Fprint(w, "vless://key@a.test:443#A")
	}))
	defer server.Close()
	if _, err := c.AddProfileFromURI(server.URL); err != nil {
		t.Fatal(err)
	}
	pf, _ := c.profiles.Read()
	stale := pf.Subscriptions[0]
	count := requests.Load()
	c.syncSubscriptions(c.subscriptionContext(), true, true)
	if requests.Load() != count {
		t.Fatal("resume/broken bypassed cooldown")
	}
	c.operationMu.Lock()
	pf.Subscriptions[0].LastAttempt = time.Now().Add(-subscriptionMinimumInterval - time.Second).Unix()
	_ = c.profiles.Write(pf)
	c.operationMu.Unlock()
	c.syncSubscriptions(c.subscriptionContext(), false, false)
	if requests.Load() != count {
		t.Fatal("normal polling ran before the 5-minute deadline")
	}
	c.syncSubscriptions(c.subscriptionContext(), true, false)
	if requests.Load() != count+1 {
		t.Fatal("resume did not accelerate stale data")
	}
	current, _ := c.profiles.Read()
	c.applySubscriptionResponse(c.subscriptionContext(), stale, subscription.Imported[[]profile.Profile]{Metadata: &subscription.Metadata{Title: "stale"}}, nil, time.Now().Unix())
	after, _ := c.profiles.Read()
	if after.Subscriptions[0].Revision != current.Subscriptions[0].Revision || after.Subscriptions[0].Info.Title == "stale" {
		t.Fatal("stale HTTP response overwrote newer state")
	}
}
func TestShutdownCancelsPendingSubscriptionHTTP(t *testing.T) {
	c, _ := initSyncCore(t)
	var block atomic.Bool
	entered := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if block.Load() {
			entered <- struct{}{}
			<-r.Context().Done()
			return
		}
		fmt.Fprint(w, "vless://key@a.test:443#A")
	}))
	defer server.Close()
	if _, err := c.AddProfileFromURI(server.URL); err != nil {
		t.Fatal(err)
	}
	block.Store(true)
	c.operationMu.Lock()
	pf, _ := c.profiles.Read()
	pf.Subscriptions[0].NextAttempt = 0
	_ = c.profiles.Write(pf)
	c.operationMu.Unlock()
	c.SubscriptionWake()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("scheduler did not fetch")
	}
	done := make(chan error, 1)
	go func() { done <- c.Shutdown() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown waited on provider timeout")
	}
}

func TestLegacySourceReimportAndAutomaticRestartRefresh(t *testing.T) {
	c, _ := initSyncCore(t)
	old := profile.SubscriptionInfo{ID: "legacy-group", Title: "Old"}
	draft, err := xray.Decode("vless://key@a.test:443#A")
	if err != nil {
		t.Fatal(err)
	}
	draft[0].Name = "stable"
	draft[0].Label = "Local name"
	draft[0].Subscription = &old
	if err := c.profiles.Write(&profile.File{Active: "stable", Profiles: draft}); err != nil {
		t.Fatal(err)
	}
	requests := make(chan struct{}, 10)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Profile-Title", "Current")
		fmt.Fprint(w, "vless://key@a.test:443#A")
		requests <- struct{}{}
	}))
	defer server.Close()
	if _, err := c.AddProfileFromURI(server.URL); err != nil {
		t.Fatal(err)
	}
	<-requests
	pf, _ := c.profiles.Read()
	if len(pf.Subscriptions) != 1 || pf.Subscriptions[0].ID != "legacy-group" || len(pf.Profiles) != 1 || pf.Profiles[0].Name != "stable" || pf.Profiles[0].Label != "Local name" {
		t.Fatal("legacy group was duplicated or local state lost")
	}
	if err := c.Shutdown(); err != nil {
		t.Fatal(err)
	}
	pf.Subscriptions[0].NextAttempt = 0
	if err := c.profiles.Write(pf); err != nil {
		t.Fatal(err)
	}
	next := New()
	f := &syncEngine{alternateEngine: alternateEngine{caps: engine.Capabilities{TUN: true, DomainRouting: true, IPRouting: true, AutoSelect: true, ProcessRouting: true}}}
	if err := next.InitWithEngine(c.baseDir, func(string, engine.Logger) (engine.Backend, error) { return f, nil }); err != nil {
		t.Fatal(err)
	}
	defer next.Shutdown()
	select {
	case <-requests:
	case <-time.After(time.Second):
		t.Fatal("restart did not automatically load persisted source")
	}
}

func TestSubscriptionPersistenceFailureRestoresConfiguration(t *testing.T) {
	c, f := initSyncCore(t)
	if _, err := c.AddProfileFromURI("vless://key@a.test:443#A"); err != nil {
		t.Fatal(err)
	}
	if err := c.StartVPN(); err != nil {
		t.Fatal(err)
	}
	before, _ := c.profiles.Read()
	after, _ := cloneProfiles(before)
	after.Profiles[0].Connection.URI = "vless://rotated@a.test:443#A"
	// An existing directory at the destination makes atomic replacement fail.
	blocked := filepath.Join(c.baseDir, "unwritable-profiles.json")
	if err := os.Mkdir(blocked, 0700); err != nil {
		t.Fatal(err)
	}
	c.operationMu.Lock()
	original := c.profiles
	c.profiles = profile.New(blocked)
	err := c.commitProfiles(c.subscriptionContext(), before, after)
	c.profiles = original
	c.operationMu.Unlock()
	if err == nil || !c.IsRunning() {
		t.Fatal("persistence failure stopped working VPN")
	}
	f.mu.Lock()
	uri := f.configuration.Profiles[0].Connection.URI
	f.mu.Unlock()
	if uri != before.Profiles[0].Connection.URI {
		t.Fatal("persistence failure left staged engine configuration")
	}
}

func TestUnhealthySubscriptionTriggerIsOneEventPerOutage(t *testing.T) {
	trigger := subscriptionHealthTrigger{}
	bad := HealthInfo{Status: "broken", Running: true}
	if trigger.observe(bad) || trigger.observe(bad) || !trigger.observe(bad) {
		t.Fatal("unconfirmed failure triggered a fetch")
	}
	// A cooldown defers the pending event without losing it.
	if !trigger.observe(bad) {
		t.Fatal("pending failure event was lost")
	}
	trigger.handled = true
	for i := 0; i < 100; i++ {
		if trigger.observe(bad) {
			t.Fatal("continuous failure retriggered synchronization")
		}
	}
	if trigger.observe(HealthInfo{Status: "ok", Running: true}) {
		t.Fatal("healthy state triggered fetch")
	}
	trigger.observe(bad)
	trigger.observe(bad)
	if !trigger.observe(bad) {
		t.Fatal("recovery did not rearm trigger")
	}
	if subscriptionRetryDelay(1, errors.New("timeout")) < 2*time.Minute {
		t.Fatal("failure retries faster than the agreed minimum")
	}
}
