package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"time"

	"NekkoVPN/core/engine"
	"NekkoVPN/core/profile"
	"NekkoVPN/core/subscription"
)

const subscriptionPollInterval = 5 * time.Minute
const subscriptionMinimumInterval = 2 * time.Minute

var subscriptionMetadataProfileLabel = regexp.MustCompile(`(?i)^осталось\s+дн\.?\s*\d+\b`)
var subscriptionSecret = regexp.MustCompile(`(?i)[a-z][a-z0-9+.-]*://[^\s"<>]+`)

func (c *Core) subscriptionContext() context.Context {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ctx == nil {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		return ctx
	}
	return c.ctx
}
func subscriptionDrafts(items [][]profile.Profile) []profile.Profile {
	var drafts []profile.Profile
	for _, batch := range items {
		for _, p := range batch {
			p.MetadataOnly = subscriptionMetadataProfileLabel.MatchString(p.Label)
			drafts = append(drafts, p)
		}
	}
	return drafts
}
func subscriptionInfo(id string, m *subscription.Metadata) profile.SubscriptionInfo {
	info := profile.SubscriptionInfo{ID: id}
	if m != nil {
		info.Title = m.Title
		info.UploadBytes = m.UploadBytes
		info.DownloadBytes = m.DownloadBytes
		info.TotalBytes = m.TotalBytes
		info.ExpiresAt = m.ExpiresAt
		info.UpdateIntervalSeconds = m.UpdateIntervalSeconds
		info.WebPageURL = m.WebPageURL
		info.SupportURL = m.SupportURL
	}
	return info
}
func cloneProfiles(pf *profile.File) (*profile.File, error) {
	raw, err := json.Marshal(pf)
	if err != nil {
		return nil, err
	}
	var clone profile.File
	err = json.Unmarshal(raw, &clone)
	return &clone, err
}
func ensureSubscription(pf *profile.File, sourceURL string, drafts []profile.Profile) (int, error) {
	for i, s := range pf.Subscriptions {
		if s.URL == sourceURL {
			return i, nil
		}
	}
	// Restore a legacy group's source only if exact server overlap points to
	// one unowned group. Ambiguous/shared endpoints cannot merge providers.
	owned := map[string]bool{}
	for _, s := range pf.Subscriptions {
		owned[s.ID] = true
	}
	keys := map[string]bool{}
	for _, p := range drafts {
		if !p.MetadataOnly {
			keys[profile.Identity(p).Key] = true
		}
	}
	groups := map[string]bool{}
	for _, p := range pf.Profiles {
		if p.Subscription != nil && !owned[p.Subscription.ID] && keys[profile.Identity(p).Key] {
			groups[p.Subscription.ID] = true
		}
	}
	id := ""
	if len(groups) == 1 {
		for group := range groups {
			id = group
		}
	}
	if id == "" {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return 0, err
		}
		id = "sub-" + hex.EncodeToString(random[:])
	}
	pf.Subscriptions = append(pf.Subscriptions, profile.Subscription{ID: id, URL: sourceURL})
	return len(pf.Subscriptions) - 1, nil
}

// Runtime equality ignores presentation/provider metadata and list order.
func sameProfileRuntime(a, b *profile.File) bool {
	if a.Active != b.Active || a.Auto != b.Auto || len(a.Profiles) != len(b.Profiles) {
		return false
	}
	specs := func(pf *profile.File) map[string]interface{} {
		result := map[string]interface{}{}
		for _, p := range pf.Profiles {
			result[p.Name] = struct {
				Key    string
				Kind   string
				Config map[string]interface{}
			}{profile.Identity(p).Key, p.Kind, p.Config}
		}
		return result
	}
	return reflect.DeepEqual(specs(a), specs(b))
}

// commitProfiles is the sole sync transaction boundary. Metadata-only commits
// never touch the adapter; all runtime/persistence failures restore the snapshot.
func (c *Core) commitProfiles(ctx context.Context, before, after *profile.File) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if sameProfileRuntime(before, after) {
		return c.profiles.Write(after)
	}
	policy, err := c.manualRouting()
	if err != nil {
		return err
	}
	policy, err = c.effectiveRouting(policy)
	if err != nil {
		return err
	}
	configuration := func(pf *profile.File) engine.Configuration {
		return engine.Configuration{Profiles: pf.Profiles, ActiveProfile: pf.Active, AutoSelect: pf.Auto, Routing: policy}
	}
	next, old := configuration(after), configuration(before)
	if err := engine.ValidateConfiguration(next, c.backend.Info().Capabilities); err != nil {
		return err
	}
	pending, err := c.profiles.Stage(after)
	if err != nil {
		return err
	}
	defer pending.Discard()
	running := c.backend.IsRunning()
	rollback := func(cause error, restart bool) error {
		configureErr := c.backend.Configure(old)
		var startErr error
		if restart && configureErr == nil && ctx.Err() == nil {
			startErr = c.backend.Start(ctx)
		}
		return errors.Join(cause, configureErr, startErr)
	}
	if err := c.backend.Configure(next); err != nil {
		return rollback(err, false)
	}
	if running {
		if err := c.backend.Stop(); err != nil {
			return rollback(err, !c.backend.IsRunning())
		}
		if len(after.Profiles) > 0 {
			if err := c.backend.Start(ctx); err != nil {
				stopErr := c.backend.Stop()
				return rollback(errors.Join(err, stopErr), stopErr == nil)
			}
		}
	}
	// Publish only after successful runtime application: UI readers must never
	// observe a candidate that is about to be rolled back after Start fails.
	if err := pending.Commit(); err != nil {
		var stopErr error
		if running && len(after.Profiles) > 0 {
			stopErr = c.backend.Stop()
		}
		return rollback(errors.Join(err, stopErr), running && stopErr == nil)
	}
	present := map[string]bool{}
	for _, p := range after.Profiles {
		present[p.Name] = true
	}
	for _, p := range before.Profiles {
		if !present[p.Name] {
			c.uptime.Forget(p.Name)
		}
	}
	c.uptime.Save()
	return nil
}

// SubscriptionWake is a nonblocking lifecycle hint, not a manual refresh UI.
// The scheduler still enforces cooldown/backoff across repeated focus events.
func (c *Core) SubscriptionWake() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.subscriptionWake != nil {
		select {
		case c.subscriptionWake <- struct{}{}:
		default:
		}
	}
}

// A continuous outage creates one event; cooldown may defer it, and recovery
// rearms it. This prevents persistent failure from becoming aggressive polling.
type subscriptionHealthTrigger struct {
	badTicks int
	handled  bool
}

func (t *subscriptionHealthTrigger) observe(info HealthInfo) bool {
	if info.Status == "broken" || (info.Running && info.Status == "connecting") {
		if t.badTicks < 3 {
			t.badTicks++
		}
	} else {
		t.badTicks = 0
		t.handled = false
	}
	return t.badTicks >= 3 && !t.handled
}

func (c *Core) runSubscriptions(ctx context.Context, wake <-chan struct{}) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	c.syncSubscriptions(ctx, true, false)
	trigger := subscriptionHealthTrigger{}
	for {
		urgent := false
		select {
		case <-ctx.Done():
			return
		case <-wake:
			urgent = true
		case <-ticker.C:
		}
		brokenEvent := trigger.observe(c.health.Probe())
		if c.syncSubscriptions(ctx, urgent, brokenEvent) && brokenEvent {
			trigger.handled = true
		}
	}
}
func (c *Core) syncSubscriptions(ctx context.Context, urgent, broken bool) bool {
	handledBroken := false
	c.operationMu.Lock()
	pf, err := c.profiles.Read()
	c.operationMu.Unlock()
	if err != nil {
		c.logSubscriptionError("store", err)
		return false
	}
	activeSource := ""
	for _, p := range pf.Profiles {
		if p.Name == pf.Active && p.Subscription != nil {
			activeSource = p.Subscription.ID
		}
	}
	for _, source := range pf.Subscriptions {
		if ctx.Err() != nil {
			return handledBroken
		}
		now := time.Now().Unix()
		accelerated := (urgent || (broken && (pf.Auto || source.ID == activeSource))) && source.Failures == 0 && now-source.LastAttempt >= int64(subscriptionMinimumInterval/time.Second)
		if now < source.NextAttempt && !accelerated {
			continue
		}
		deviceID, err := c.subscriptionDeviceID()
		if err != nil {
			c.logSubscriptionError(source.ID, err)
			continue
		}
		imported, fetchErr := subscription.Fetch(ctx, source.URL, deviceID, c.backend.Decode)
		if ctx.Err() != nil {
			return handledBroken
		}
		c.applySubscriptionResponse(ctx, source, imported, fetchErr, now)
		if broken && (pf.Auto || source.ID == activeSource) {
			handledBroken = true
		}
	}
	return handledBroken
}
func (c *Core) applySubscriptionResponse(ctx context.Context, requested profile.Subscription, imported subscription.Imported[[]profile.Profile], fetchErr error, attempt int64) {
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	if ctx.Err() != nil {
		return
	}
	before, err := c.profiles.Read()
	if err != nil {
		c.logSubscriptionError(requested.ID, err)
		return
	}
	index := -1
	for i, s := range before.Subscriptions {
		if s.ID == requested.ID && s.URL == requested.URL && s.Revision == requested.Revision {
			index = i
			break
		}
	}
	if index < 0 {
		return
	} // A newer import won while the HTTP request was in flight.
	after, err := cloneProfiles(before)
	if err != nil {
		c.logSubscriptionError(requested.ID, err)
		return
	}
	source := &after.Subscriptions[index]
	source.LastAttempt = attempt
	source.Revision++
	if fetchErr == nil {
		source.Info = subscriptionInfo(source.ID, imported.Metadata)
		_, fetchErr = profile.ReconcileSubscription(after, source, subscriptionDrafts(imported.Items))
	}
	if fetchErr == nil {
		source.LastSuccess = time.Now().Unix()
		source.NextAttempt = source.LastSuccess + int64(subscriptionPollInterval/time.Second)
		source.Failures = 0
		fetchErr = c.commitProfiles(ctx, before, after)
		if fetchErr == nil {
			return
		}
	}
	// Record scheduling separately on the last accepted snapshot after rollback.
	source = &before.Subscriptions[index]
	source.LastAttempt = attempt
	source.Revision++
	source.Failures++
	delay := subscriptionRetryDelay(source.Failures, fetchErr)
	source.NextAttempt = time.Now().Add(delay).Unix()
	if err := c.profiles.Write(before); err != nil {
		fetchErr = errors.Join(fetchErr, err)
	}
	c.logSubscriptionError(requested.ID, fetchErr)
}
func subscriptionRetryDelay(failures int, err error) time.Duration {
	delay := subscriptionMinimumInterval
	for i := 1; i < failures && delay < 15*time.Minute; i++ {
		delay *= 2
	}
	if delay > 15*time.Minute {
		delay = 15 * time.Minute
	}
	var httpErr *subscription.HTTPError
	if errors.As(err, &httpErr) {
		if (httpErr.Status == 401 || httpErr.Status == 403 || httpErr.Status == 429) && delay < 15*time.Minute {
			delay = 15 * time.Minute
		}
		if httpErr.RetryAfter > delay {
			delay = httpErr.RetryAfter
		}
	}
	return delay
}
func (c *Core) logSubscriptionError(id string, err error) {
	c.logger.Logf("[subscription %s] %s", id, subscriptionSecret.ReplaceAllString(fmt.Sprint(err), "[redacted-link]"))
}
