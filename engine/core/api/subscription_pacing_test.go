package api

import (
	"NekkoVPN/core/profile"
	"testing"
	"time"
)

func TestSubscriptionPacingFollowsWorkingTunnel(t *testing.T) {
	now := time.Now().Unix()
	base := profile.Subscription{URL: "https://sub.hikkari.net/sub/test", LastSuccess: now - 6*60, LastAttempt: now - 6*60, NextAttempt: now + 24*60}
	if subscriptionRefreshDue(base, now, false, true) {
		t.Fatal("VPN-off wake bypassed 30-minute cooldown")
	}
	if !subscriptionRefreshDue(base, now, true, false) {
		t.Fatal("restored tunnel kept offline deadline")
	}
	base.LastSuccess = now - 31*60
	base.LastAttempt = base.LastSuccess
	if !subscriptionRefreshDue(base, now, false, false) {
		t.Fatal("offline refresh never becomes due")
	}
	base.LastAttempt = now - 60
	base.LastSuccess = base.LastAttempt
	if subscriptionRefreshDue(base, now, true, true) {
		t.Fatal("online urgent refresh ignored two-minute guard")
	}
	base.LastAttempt = now - 3*60
	base.LastSuccess = base.LastAttempt
	if !subscriptionRefreshDue(base, now, true, true) {
		t.Fatal("online recovery blocked")
	}
	base.Failures = 1
	base.NextAttempt = now + 40*60
	if subscriptionRefreshDue(base, now, true, true) {
		t.Fatal("recovery bypassed server/retry deadline")
	}
}

func TestSubscriptionPacingKeepsBootstrapAndOtherProviders(t *testing.T) {
	now := time.Now().Unix()
	if !subscriptionRefreshDue(profile.Subscription{URL: "https://sub.hikkari.net/sub/test"}, now, false, true) {
		t.Fatal("first subscription load blocked")
	}
	source := profile.Subscription{URL: "https://other.example/sub/test", LastSuccess: now - 6*60, LastAttempt: now - 6*60, NextAttempt: now - 60}
	if !subscriptionRefreshDue(source, now, false, false) {
		t.Fatal("other provider delayed")
	}
	if subscriptionRefreshInterval(source.URL, false) != 5*time.Minute {
		t.Fatal("other provider pacing changed")
	}
}
