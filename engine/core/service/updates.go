package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
}

func newUpdates(srv *Server) *updates {
	return &updates{
		srv:  srv,
		exe:  srv.cfg.Updater,
		st:   svcproto.UpdateState{State: svcproto.UpdateIdle},
		kick: make(chan struct{}, 1),
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
// When the swap replaced the service itself (Config.RestartAfterApply) the
// core is not brought back here: the service is about to exit, and the new
// one resumes it from the run marker — which reconnect sets.
func (u *updates) apply(reconnect bool) (from, to string, err error) {
	u.opMu.Lock()
	defer u.opMu.Unlock()
	if st := u.snapshot(); st.State != svcproto.UpdateReady {
		return "", "", errors.New("нет готового обновления")
	}
	swap := u.withCoreDown
	if u.srv.cfg.RestartAfterApply {
		swap = u.withCoreLeftDown
		if reconnect {
			_ = os.WriteFile(u.srv.runMarker(), nil, 0o644)
		}
	}
	err = swap(reconnect, func() error {
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
		return u.run("recover", func(ev map[string]interface{}) {
			if ev["event"] == "recovered" {
				result, from, to = str(ev["result"]), str(ev["from"]), str(ev["to"])
			}
		})
	}
	if moves {
		err = u.withCoreDown(false, do)
	} else {
		err = do()
	}
	if err == nil && result != "nothing" {
		u.srv.log.Logf("updates: recover %s (%s -> %s)", result, from, to)
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
	return u.run("confirm", nil)
}

func (u *updates) rollback() (from, to string, err error) {
	u.opMu.Lock()
	defer u.opMu.Unlock()
	err = u.withCoreDown(false, func() error {
		return u.run("rollback", func(ev map[string]interface{}) {
			if ev["event"] == "rolled_back" {
				from, to = str(ev["from"]), str(ev["to"])
			}
		})
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
// was running before or if start is set. Restarting reuses core\config.json —
// the last config the GUI sent — with whatever sing-box.exe app\ now holds.
func (u *updates) withCoreDown(start bool, f func() error) error {
	s := u.srv
	s.mu.Lock()
	defer s.mu.Unlock()
	wasRunning := s.core.Pid() != 0
	if wasRunning {
		_ = s.core.Stop()
	}
	err := f()
	if wasRunning || start {
		if _, statErr := os.Stat(filepath.Join(s.cfg.CoreDir, "config.json")); statErr == nil {
			if startErr := s.core.Start(); startErr != nil {
				s.log.Logf("updates: core did not come back: %v", startErr)
			}
		}
	}
	return err
}

// withCoreLeftDown stops the core around f and leaves it down: the service
// is about to be replaced and the next one resumes it (see apply).
func (u *updates) withCoreLeftDown(_ bool, f func() error) error {
	s := u.srv
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.core.Pid() != 0 {
		_ = s.core.Stop()
	}
	return f()
}

// watchTrial rolls back a version that was swapped in moments before this
// service started and is still unconfirmed at Config.TrialDeadline after the
// swap. On macOS the swap restarts the service and opens the new GUI right
// away; a GUI that runs confirms within seconds, so a version still on trial
// at the deadline never came up at all — the case no start counter can see,
// since nothing of the new version ran to count. A trial older than the
// deadline (the machine rebooted mid-trial, the GUI is simply not open) is
// left to the ordinary counting at the next GUI start.
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
	case <-time.After(time.Until(applied.Add(deadline))):
	}
	if u.pendingSince().IsZero() {
		return
	}
	u.srv.log.Logf("updates: the version on trial was never confirmed, rolling back")
	if _, _, err := u.rollback(); err != nil {
		u.srv.log.Logf("updates: rollback: %v", err)
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
