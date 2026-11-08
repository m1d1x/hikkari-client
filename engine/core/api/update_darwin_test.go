//go:build darwin

package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"NekkoVPN/core/svcclient"
	"NekkoVPN/core/svcproto"
)

// The trial's messages are delivered, not attempted. The swap restarts the
// service under the GUI it just opened, and the one report that settles a
// trial must not be lost to the restart's socket gap: a GUI that is up keeps
// reporting until the service answers (dima-001, 08.11: 1.0.68 rolled back
// with 0 failed starts).
func TestTrialReportRetriesUntilTheServiceAnswers(t *testing.T) {
	trialReportRetryEvery = time.Millisecond
	t.Cleanup(func() { trialReportRetryEvery = time.Second })
	calls := 0
	resp, err := trialReport(context.Background(), 5*time.Second, func() (svcproto.Response, error) {
		calls++
		if calls < 3 {
			return svcproto.Response{}, svcclient.ErrNoService
		}
		return svcproto.Response{Recovery: "on_trial", From: "1.0.0", To: "1.0.1"}, nil
	})
	if err != nil {
		t.Fatalf("trial report: %v", err)
	}
	if resp.Recovery != "on_trial" || resp.To != "1.0.1" {
		t.Fatalf("trial report: %+v", resp)
	}
	if calls != 3 {
		t.Fatalf("trial report stopped after %d calls", calls)
	}
}

func TestTrialReportStopsWhenTheServiceIsGone(t *testing.T) {
	trialReportRetryEvery = time.Millisecond
	t.Cleanup(func() { trialReportRetryEvery = time.Second })
	calls := 0
	start := time.Now()
	_, err := trialReport(context.Background(), 20*time.Millisecond, func() (svcproto.Response, error) {
		calls++
		return svcproto.Response{}, svcclient.ErrNoService
	})
	if !errors.Is(err, svcclient.ErrNoService) {
		t.Fatalf("a service that stays away: %v", err)
	}
	if calls < 2 {
		t.Fatalf("the report gave up without retrying: %d calls", calls)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("the budget was not respected: %v", elapsed)
	}
}

// An answer is final whatever it says: a retry loop cannot wish a broken
// updater into working, and a refusal is the service's verdict.
func TestTrialReportAnswersAreFinal(t *testing.T) {
	calls := 0
	_, err := trialReport(context.Background(), time.Second, func() (svcproto.Response, error) {
		calls++
		return svcproto.Response{}, errors.New("the updater refused")
	})
	if err == nil || calls != 1 {
		t.Fatalf("an answered refusal must not be retried: %d calls, %v", calls, err)
	}
}

func TestTrialReportStopsWhenTheAppExits(t *testing.T) {
	trialReportRetryEvery = time.Millisecond
	t.Cleanup(func() { trialReportRetryEvery = time.Second })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	_, err := trialReport(ctx, time.Second, func() (svcproto.Response, error) {
		calls++
		return svcproto.Response{}, svcclient.ErrNoService
	})
	if !errors.Is(err, svcclient.ErrNoService) || calls != 1 {
		t.Fatalf("a cancelled app must not keep reporting: %d calls, %v", calls, err)
	}
}
