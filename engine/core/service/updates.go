package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"NekkoVPN/core/svcproto"
)

const (
	// First check shortly after boot, not at it: the first seconds belong to
	// bringing the machine (and maybe the tunnel) up.
	firstCheckAfter = time.Minute
	// A check is a ~100-byte probe; the manifest is fetched only when the
	// probe names a new version (and then answered 304 from cache when
	// unchanged). Half an hour costs the channel nothing.
	checkEvery = 30 * time.Minute
	// check_update from a GUI launch is ignored this soon after a check.
	checkThrottle = 5 * time.Minute

	// apply is retried this many times, a second apart.
	applyAttempts = 3
)

// updates drives hikkari-updater for the service.
//
// Locking: opMu serialises every updater invocation (the staging directory
// allows one at a time). Operations that touch the core take the server's mu
// INSIDE opMu, never the other way round — so a long download holds opMu but
// never blocks the GUI's start/stop, which take only mu.
type updates struct {
	srv *Server
	exe string

	opMu sync.Mutex

	mu sync.Mutex
	st svcproto.UpdateState

	kick chan struct{}

	// guiArrived is closed when a GUI of the version on trial has reported in
	// (recover_update answered on_trial): watchTrial hands over to it.
	guiArrived     chan struct{}
	guiArrivedOnce sync.Once
}

func newUpdates(srv *Server) *updates {
	return &updates{
		srv:        srv,
		exe:        srv.cfg.Updater,
		st:         svcproto.UpdateState{State: svcproto.UpdateIdle},
		kick:       make(chan struct{}, 1),
		guiArrived: make(chan struct{}),
	}
}

func (u *updates) snapshot() *svcproto.UpdateState {
	u.mu.Lock()
	defer u.mu.Unlock()
	st := u.st
	return &st
}

func (u *updates) set(f func(st *svcproto.UpdateState)) {
	u.mu.Lock()
	defer u.mu.Unlock()
	f(&u.st)
}

// loop checks on a schedule and on request until ctx ends.
func (u *updates) loop(ctx context.Context) {
	if _, err := os.Stat(u.exe); err != nil {
		u.srv.log.Logf("updates: %s missing, updates unavailable", u.exe)
		u.set(func(st *svcproto.UpdateState) { st.State = svcproto.UpdateUnavailable })
		return
	}
	u.loadStatus()
	timer := time.NewTimer(firstCheckAfter)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-u.kick:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}
		u.check()
		if u.snapshot().State == svcproto.UpdateUnavailable {
			return
		}
		timer.Reset(checkEvery)
	}
}

// requestCheck asks the loop for a check now, unless one just happened.
func (u *updates) requestCheck() {
	st := u.snapshot()
	if st.State == svcproto.UpdateUnavailable {
		return
	}
	if st.State != svcproto.UpdateError && time.Since(time.Unix(st.CheckedAt, 0)) < checkThrottle {
		return
	}
	select {
	case u.kick <- struct{}{}:
	default:
	}
}

// loadStatus seeds the state from disk: what is installed, what is staged.
func (u *updates) loadStatus() {
	u.opMu.Lock()
	defer u.opMu.Unlock()
	_ = u.run("status", func(ev map[string]interface{}) {
		if ev["event"] != "status" {
			return
		}
		u.set(func(st *svcproto.UpdateState) {
			st.Installed = str(ev["installed"])
			if ready := str(ev["ready"]); ready != "" {
				st.State, st.Version = svcproto.UpdateReady, ready
			}
		})
	})
}

func (u *updates) check() {
	u.opMu.Lock()
	defer u.opMu.Unlock()
	u.srv.log.Logf("updates: checking")
	err := u.run("update", func(ev map[string]interface{}) {
		u.set(func(st *svcproto.UpdateState) {
			switch ev["event"] {
			case "checked":
				st.Installed = str(ev["installed"])
				switch ev["result"] {
				case "available", "mandatory":
					if !(st.State == svcproto.UpdateReady && st.Version == str(ev["version"])) {
						st.State = svcproto.UpdateDownloading
					}
					st.Version = str(ev["version"])
					st.Mandatory = ev["result"] == "mandatory"
				default:
					st.State, st.Version, st.Mandatory = svcproto.UpdateIdle, "", false
				}
			case "progress":
				st.BytesDone, st.BytesAll = num(ev["bytes_done"]), num(ev["bytes_total"])
			case "ready":
				st.State, st.Version = svcproto.UpdateReady, str(ev["version"])
				st.Mandatory, _ = ev["mandatory"].(bool)
			}
		})
	})
	u.set(func(st *svcproto.UpdateState) {
		st.CheckedAt = time.Now().Unix()
		var ue *updaterError
		switch {
		case errors.As(err, &ue) && ue.code == "no_channel":
			st.State = svcproto.UpdateUnavailable
		case err != nil:
			st.Error = err.Error()
			if st.State != svcproto.UpdateReady {
				st.State = svcproto.UpdateError
			}
		default:
			st.Error = ""
		}
	})
	st := u.snapshot()
	u.srv.log.Logf("updates: %s installed=%s version=%s err=%q", st.State, st.Installed, st.Version, st.Error)
}

// apply swaps the staged version in. The core is stopped around it: on
// Windows sing-box.exe inside app\ would hold the directory open, and on both
// platforms the core must come back as the new version's binary.
//
// A wanted core does not come back here: it would run the new binary on the old
// release's rules until the new GUI restarted it. It stays down for the GUI's
// config, so an update costs one break (Server.beginAwaitingConfig). Xray has no
// such GUI and comes back as before. A failed swap changed nothing: the core
// comes back at once.
func (u *updates) apply(reconnect bool) (from, to string, err error) {
	u.opMu.Lock()
	defer u.opMu.Unlock()
	if st := u.snapshot(); st.State != svcproto.UpdateReady {
		return "", "", errors.New("нет готового обновления")
	}
	err = u.swap(reconnect, func() error {
		var err error
		// A file inside app\ held for a moment after the core exits (an
		// antivirus scanning it, a straggling handle) fails the first
		// rename. That failure moves nothing and leaves no trial record, so
		// trying again is safe.
		for attempt := 1; attempt <= applyAttempts; attempt++ {
			err = u.run("apply", func(ev map[string]interface{}) {
				if ev["event"] == "applied" {
					from, to = str(ev["from"]), str(ev["to"])
				}
			})
			if err == nil {
				return nil
			}
			u.srv.log.Logf("updates: apply attempt %d/%d: %v", attempt, applyAttempts, err)
			time.Sleep(time.Second)
		}
		return err
	})
	if err != nil {
		return "", "", err
	}
	u.set(func(st *svcproto.UpdateState) {
		*st = svcproto.UpdateState{State: svcproto.UpdateIdle, Installed: to, CheckedAt: st.CheckedAt}
	})
	u.srv.onVersion(to)
	u.srv.log.Logf("updates: applied %s -> %s (reconnect=%v)", from, to, reconnect)
	return from, to, nil
}

// swap is apply's handling of the core around the updater.
func (u *updates) swap(reconnect bool, f func() error) error {
	s := u.srv
	s.mu.Lock()
	defer s.mu.Unlock()
	running, _ := s.running()
	wanted := running != "" || reconnect || s.wanted()
	s.keepConfigForRollback()
	if running != "" {
		s.engine = running
		_ = s.stopCores()
	}
	if err := f(); err != nil {
		if wanted {
			if startErr := s.runCore(s.engine); startErr != nil {
				s.log.Logf("updates: core did not come back: %v", startErr)
			}
		}
		return err
	}
	if !wanted {
		return nil
	}
	if !s.wanted() {
		_ = os.WriteFile(s.runMarker(), []byte(s.engine), 0o644)
	}
	switch {
	case s.engine == svcproto.EngineSingBox:
		s.beginAwaitingConfig()
		s.log.Logf("updates: core stays down until the app sends the new release's config")
	case !s.cfg.RestartAfterApply:
		if err := s.runCore(s.engine); err != nil {
			s.log.Logf("updates: core did not come back: %v", err)
		}
	}
	return nil
}

// recover runs at every launch. It only takes the core down when directories
// are about to move, so an ordinary launch never drops the tunnel.
func (u *updates) recover() (result, from, to string, err error) {
	u.opMu.Lock()
	defer u.opMu.Unlock()
	// Only a recover that renames directories (a rollback, or repairing an
	// interrupted swap) needs the core down; one that just counts a start of
	// a version on trial must not drop the tunnel.
	moves := false
	if statusErr := u.run("status", func(ev map[string]interface{}) {
		moves, _ = ev["recover_moves"].(bool)
	}); statusErr != nil {
		moves = true // app\ unreadable: an interrupted swap to repair
	}
	do := func() error {
		err := u.run("recover", func(ev map[string]interface{}) {
			if ev["event"] == "recovered" {
				result, from, to = str(ev["result"]), str(ev["from"]), str(ev["to"])
			}
		})
		if err == nil && result == "rolled_back" {
			u.srv.restoreConfigFromRollback()
		}
		return err
	}
	if moves {
		err = u.withCoreDown(false, do)
	} else {
		err = do()
	}
	if err == nil && result != "nothing" {
		u.srv.log.Logf("updates: recover %s (%s -> %s)", result, from, to)
	}
	if err == nil && result == "on_trial" {
		u.guiArrivedOnce.Do(func() { close(u.guiArrived) })
	}
	if result == "rolled_back" {
		u.srv.onVersion(from)
		u.set(func(st *svcproto.UpdateState) {
			st.State, st.Installed, st.Version = svcproto.UpdateIdle, from, ""
		})
	}
	return result, from, to, err
}

func (u *updates) confirm() error {
	u.opMu.Lock()
	defer u.opMu.Unlock()
	if err := u.run("confirm", nil); err != nil {
		return err
	}
	u.srv.dropConfigForRollback()
	return nil
}

func (u *updates) rollback() (from, to string, err error) {
	u.opMu.Lock()
	defer u.opMu.Unlock()
	err = u.withCoreDown(false, func() error {
		err := u.run("rollback", func(ev map[string]interface{}) {
			if ev["event"] == "rolled_back" {
				from, to = str(ev["from"]), str(ev["to"])
			}
		})
		if err == nil {
			u.srv.restoreConfigFromRollback()
		}
		return err
	})
	if err == nil {
		u.srv.onVersion(from)
		u.srv.log.Logf("updates: rolled back %s -> %s", to, from)
		u.set(func(st *svcproto.UpdateState) {
			st.State, st.Installed, st.Version = svcproto.UpdateIdle, from, ""
		})
	}
	return from, to, err
}

// withCoreDown stops the core around f and starts it again afterwards if it
// is wanted: it was running, start is set, or it stayed down for a config that
// will now not come. Restarting reuses the stored config for the last engine,
// with whatever binary the installation now holds — unless a newer release
// wrote that config: a rollback with no copy to restore (the swap was made by
// a service from before keepConfigForRollback) leaves the newer release's
// config, which the older core may refuse field by field. The core then stays
// wanted and down, and the GUI of the installed release starts it.
func (u *updates) withCoreDown(start bool, f func() error) error {
	s := u.srv
	s.mu.Lock()
	defer s.mu.Unlock()
	running, _ := s.running()
	if running != "" {
		_ = s.stopCores()
	}
	err := f()
	s.endAwaitingConfig()
	if running != "" || start || s.wanted() {
		if newer, installed := s.configOfNewerRelease(s.engine); newer != "" {
			if !s.wanted() {
				_ = os.WriteFile(s.runMarker(), []byte(s.engine), 0o644)
			}
			s.log.Logf("updates: the stored %s config is from %s, newer than the installed %s: the core waits for the app's config", s.engine, newer, installed)
			return err
		}
		if _, statErr := os.Stat(s.configPath(s.engine)); statErr == nil {
			if startErr := s.runCore(s.engine); startErr != nil {
				s.log.Logf("updates: core did not come back: %v", startErr)
			}
		}
	}
	return err
}

// watchTrial rolls back a version that was swapped in moments before this
// service started when no GUI of it has reported in by Config.TrialDeadline
// after the swap. On macOS the swap restarts the service and opens the new
// GUI; a new version whose GUI never gets as far as recover_update never came
// up at all — the case no start counter can see, since nothing of the new
// version ran to count.
//
// A GUI that did report in owns the trial from there: it confirms once it has
// stayed up (api.trialWindow), and one that dies first is counted against the
// version at the next start. The deadline is not a confirmation deadline: the
// first launch of a freshly swapped bundle can take most of a minute and more
// (Gatekeeper reads it whole), and rolling back a version that came up only
// to be slow leaves the new GUI on the old core (tester-004, 06.11: 86 s to
// the first request, 90 s deadline, a rollback with 0 failed starts).
//
// A trial older than the deadline (the machine rebooted mid-trial, the GUI is
// simply not open) is left to the ordinary counting at the next GUI start.
func (u *updates) watchTrial(ctx context.Context) {
	deadline := u.srv.cfg.TrialDeadline
	if deadline <= 0 {
		return
	}
	applied := u.pendingSince()
	if applied.IsZero() || time.Since(applied) >= deadline {
		return
	}
	u.srv.log.Logf("updates: a version is on trial since %s, watching it for %s", applied.Format(time.RFC3339), deadline)
	select {
	case <-ctx.Done():
		return
	case <-u.guiArrived:
		u.srv.log.Logf("updates: the version on trial has its GUI up, the GUI confirms it")
		return
	case <-time.After(time.Until(applied.Add(deadline))):
	}
	if u.pendingSince().IsZero() {
		return
	}
	u.srv.log.Logf("updates: no GUI of the version on trial came up within %s, rolling back", deadline)
	if _, _, err := u.rollback(); err != nil {
		u.srv.log.Logf("updates: rollback: %v", err)
		return
	}
	if u.srv.cfg.RestartAfterApply {
		// The rollback replaced this binary too.
		u.srv.requestRestart()
	}
}

// pendingSince is when the version on trial was swapped in; zero if none is.
func (u *updates) pendingSince() time.Time {
	u.opMu.Lock()
	defer u.opMu.Unlock()
	var at time.Time
	_ = u.run("status", func(ev map[string]interface{}) {
		if p, ok := ev["pending"].(map[string]interface{}); ok {
			at = time.Unix(num(p["applied_at"]), 0)
		}
	})
	return at
}

// settle hands over to Config.SyncBinaries once no version is on trial: a
// version on trial may still be rolled back, and the binaries outside the
// tree must not run ahead of it. Runs at start, after a confirmation or
// rollback, and after the core stops; with nothing to bring in line it costs two
// file hashes. Replacing the service restarts it, so while the core is up or wanted
// its binary waits for one of those moments: an update has already cost its
// one break. now: restarting costs nothing (no core has run yet) or is worth it (a
// config this service refuses), so the service is free to go.
//
// It reports a restart it has asked for.
func (u *updates) settle(now bool) bool {
	sync := u.srv.cfg.SyncBinaries
	if sync == nil {
		return false
	}
	u.opMu.Lock()
	defer u.opMu.Unlock()
	onTrial := false
	if err := u.run("status", func(ev map[string]interface{}) {
		_, onTrial = ev["pending"].(map[string]interface{})
	}); err != nil {
		u.srv.log.Logf("updates: not syncing binaries, status failed: %v", err)
		return false
	}
	if onTrial {
		return false
	}
	u.srv.mu.Lock()
	restart := sync(now || u.srv.idle())
	u.srv.mu.Unlock()
	if restart {
		u.srv.log.Logf("service: restarting into the updated binaries")
		u.srv.requestRestart()
	}
	return restart
}

// updaterError is a failed hikkari-updater command, with its error code.
type updaterError struct {
	code, msg string
}

func (e *updaterError) Error() string { return e.msg }

// run executes one hikkari-updater command, feeding every JSON event to
// onEvent. A non-zero exit is an error carrying the command's own message.
func (u *updates) run(command string, onEvent func(map[string]interface{})) error {
	cmd := exec.Command(u.exe, append([]string{command}, u.srv.cfg.UpdaterArgs...)...)
	if u.srv.cfg.PrepareCmd != nil {
		u.srv.cfg.PrepareCmd(cmd)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting hikkari-updater: %w", err)
	}
	go u.srv.log.PipeLines("updater", stderr)

	var failure *updaterError
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var ev map[string]interface{}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		if ev["event"] == "error" {
			failure = &updaterError{code: str(ev["code"]), msg: str(ev["message"])}
			continue
		}
		if onEvent != nil {
			onEvent(ev)
		}
	}
	waitErr := cmd.Wait()
	if failure != nil {
		return failure
	}
	if waitErr != nil {
		return fmt.Errorf("hikkari-updater %s: %w", command, waitErr)
	}
	return nil
}

func str(v interface{}) string {
	s, _ := v.(string)
	return s
}

func num(v interface{}) int64 {
	f, _ := v.(float64)
	return int64(f)
}
