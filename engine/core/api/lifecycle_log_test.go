package api

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"NekkoVPN/core/engine"
)

// Validate the actual stop boundary: intent must already be in the log even
// if the backend hangs or the application terminates during Stop.
type lifecycleEngine struct {
	routingEngine
	beforeStop func()
}

func (f *lifecycleEngine) Stop() error {
	if f.beforeStop != nil {
		f.beforeStop()
	}
	return f.routingEngine.Stop()
}

func TestLifecycleRecordsIntentBeforeStopAndFailedRestartAfterRecovery(t *testing.T) {
	f := &lifecycleEngine{routingEngine: routingEngine{alternateEngine: alternateEngine{caps: engine.Capabilities{TUN: true, DomainRouting: true, IPRouting: true, ProcessRouting: true, FolderRouting: true}}}}
	c := New()
	if err := c.InitWithEngine(manualRoutingDir(t), func(string, engine.Logger) (engine.Backend, error) { return f, nil }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.beforeStop = nil; _ = c.Shutdown() })
	if _, err := c.AddProfileFromURI("Server"); err != nil {
		t.Fatal(err)
	}
	if err := c.StartVPN(); err != nil {
		t.Fatal(err)
	}
	f.beforeStop = func() {
		log := c.logger.Tail()
		for _, want := range []string{"reason=manual-routing running=true", "reason=manual-routing/stop running=true", "changed=bypass.domains:0->1", "api.(*Core).flushRoutingLocked"} {
			if !strings.Contains(log, want) {
				t.Fatalf("missing intent before Stop: %q\n%s", want, log)
			}
		}
	}
	f.mu.Lock()
	f.startFailures = 1
	f.mu.Unlock()
	if err := c.AddRule("bypass", "domains", "private-destination.example"); err != nil {
		t.Fatal(err)
	}
	if err := c.FlushRouting(); err == nil {
		t.Fatal("expected injected failure")
	}
	log := c.logger.Tail()
	begin := regexp.MustCompile(`\[lifecycle\] begin id=(\d+) reason=manual-routing running=true`).FindStringSubmatch(log)
	if len(begin) != 2 {
		t.Fatal(log)
	}
	end := regexp.MustCompile(`\[lifecycle\] end id=` + begin[1] + ` reason=manual-routing running=true took=\S+ ok=false err=".*injected Start failure`).FindString(log)
	if end == "" {
		t.Fatalf("failed candidate/recovered runtime was not distinguished:\n%s", log)
	}
	for _, line := range strings.Split(log, "\n") {
		if strings.Contains(line, "[lifecycle]") && strings.Contains(line, "private-destination.example") {
			t.Fatal("routing values leaked", line)
		}
	}
}

func TestLifecycleSubscriptionRuntimeChangesAndMetadataOnlyRefresh(t *testing.T) {
	c, _ := initSyncCore(t)
	if _, err := c.AddProfileFromURI("vless://private-key@a.test:443#A"); err != nil {
		t.Fatal(err)
	}
	if err := c.StartVPN(); err != nil {
		t.Fatal(err)
	}
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	before, err := c.profiles.Read()
	if err != nil {
		t.Fatal(err)
	}
	after, err := cloneProfiles(before)
	if err != nil {
		t.Fatal(err)
	}
	after.Profiles[0].Connection.URI = "vless://rotated-private-key@a.test:443#A"
	if err := c.commitProfiles(context.Background(), before, after); err != nil {
		t.Fatal(err)
	}
	log := c.logger.Tail()
	for _, want := range []string{"reason=profiles-runtime-change running=true", "added=0 removed=0 changed=1 active_changed=false auto_changed=false", "api.(*Core).commitProfiles", "ok=true"} {
		if !strings.Contains(log, want) {
			t.Fatalf("missing %q:\n%s", want, log)
		}
	}
	count := strings.Count(log, "[lifecycle]")
	metadata, err := cloneProfiles(after)
	if err != nil {
		t.Fatal(err)
	}
	metadata.Profiles[0].Label = "Renamed"
	if err := c.commitProfiles(context.Background(), after, metadata); err != nil {
		t.Fatal(err)
	}
	if strings.Count(c.logger.Tail(), "[lifecycle]") != count {
		t.Fatal("metadata-only refresh reported a restart")
	}
	for _, line := range strings.Split(log, "\n") {
		if strings.Contains(line, "[lifecycle]") && (strings.Contains(line, "private-key") || strings.Contains(line, "a.test")) {
			t.Fatal("profile values leaked", line)
		}
	}
}
