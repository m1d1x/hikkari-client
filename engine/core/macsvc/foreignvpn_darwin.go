package macsvc

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"NekkoVPN/core/foreignvpn"
)

const (
	scutil = "/usr/sbin/scutil"
	psBin  = "/bin/ps"
	// A client gets foreignTermWait to quit on SIGTERM, then
	// foreignKillWait after SIGKILL.
	foreignTermWait = 3 * time.Second
	foreignKillWait = 2 * time.Second
	// foreignVPNStopWait bounds the wait for a system VPN configuration to
	// report itself disconnected.
	foreignVPNStopWait = 5 * time.Second
)

// foreignSystem is foreignvpn.System on macOS: processes from ps, VPN
// configurations from scutil. macOS has no Windows services.
type foreignSystem struct{}

func (foreignSystem) Services() ([]foreignvpn.Service, error) { return nil, nil }

// Processes lists every process with its executable. comm is the full path
// on macOS, and it stays the executable even when argv[0] is rewritten.
func (foreignSystem) Processes() ([]foreignvpn.Process, error) {
	out, err := exec.Command(psBin, "-axo", "pid=,comm=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	return parseProcesses(string(out)), nil
}

func parseProcesses(out string) []foreignvpn.Process {
	var procs []foreignvpn.Process
	for _, line := range strings.Split(out, "\n") {
		pid, path, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		n, err := strconv.ParseUint(pid, 10, 32)
		if err != nil {
			continue
		}
		procs = append(procs, foreignvpn.Process{PID: uint32(n), Path: strings.TrimSpace(path)})
	}
	return procs
}

// executable is what pid runs now, "" when it is gone.
func executable(pid uint32) string {
	out, err := exec.Command(psBin, "-p", strconv.FormatUint(uint64(pid), 10), "-o", "comm=").Output()
	if err != nil {
		return "" // ps exits 1 for a PID that does not exist
	}
	return strings.TrimSpace(string(out))
}

// KillProcess asks with SIGTERM first: a client that quits by itself takes
// its routes and resolver settings down cleanly. SIGKILL after that; a
// utun goes with the last process holding it either way. Between the
// signals the executable is checked again, so a PID that was reused is not
// signalled.
func (foreignSystem) KillProcess(p foreignvpn.Process) error {
	for _, step := range []struct {
		sig  syscall.Signal
		wait time.Duration
	}{{syscall.SIGTERM, foreignTermWait}, {syscall.SIGKILL, foreignKillWait}} {
		if executable(p.PID) != p.Path {
			return nil
		}
		if err := syscall.Kill(int(p.PID), step.sig); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				return nil
			}
			return fmt.Errorf("kill %s: %w", step.sig, err)
		}
		for deadline := time.Now().Add(step.wait); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
			if executable(p.PID) != p.Path {
				return nil
			}
		}
	}
	return errors.New("process did not exit in time")
}

// ConnectedVPNs reads `scutil --nc list`: Network Extension VPNs (the App
// Store WireGuard, Outline and the like) and IKEv2/L2TP services.
func (foreignSystem) ConnectedVPNs() ([]foreignvpn.SystemVPN, error) {
	out, err := exec.Command(scutil, "--nc", "list").Output()
	if err != nil {
		return nil, fmt.Errorf("scutil --nc list: %w", err)
	}
	return parseNCList(string(out)), nil
}

// parseNCList picks the connected and connecting services out of
//
//   - (Connected)      7A3C…-1F2E VPN (com.wireguard.macos) "Дом"      [VPN/com.wireguard.macos]
//
// The ID is the field after the state; the name is the quoted one.
func parseNCList(out string) []foreignvpn.SystemVPN {
	var vpns []foreignvpn.SystemVPN
	for _, line := range strings.Split(out, "\n") {
		open := strings.Index(line, "(")
		closing := strings.Index(line, ")")
		if open < 0 || closing < open {
			continue
		}
		switch line[open+1 : closing] {
		case "Connected", "Connecting":
		default:
			continue
		}
		fields := strings.Fields(line[closing+1:])
		if len(fields) == 0 {
			continue
		}
		v := foreignvpn.SystemVPN{ID: fields[0], Name: fields[0]}
		if first := strings.Index(line, `"`); first >= 0 {
			if last := strings.LastIndex(line, `"`); last > first {
				v.Name = line[first+1 : last]
			}
		}
		vpns = append(vpns, v)
	}
	return vpns
}

func (foreignSystem) StopVPN(v foreignvpn.SystemVPN) error {
	if err := exec.Command(scutil, "--nc", "stop", v.ID).Run(); err != nil {
		return fmt.Errorf("scutil --nc stop: %w", err)
	}
	for deadline := time.Now().Add(foreignVPNStopWait); ; time.Sleep(200 * time.Millisecond) {
		out, err := exec.Command(scutil, "--nc", "status", v.ID).Output()
		if err == nil {
			state, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
			if state == "Disconnected" {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return errors.New("still connected")
		}
	}
}

func (foreignSystem) StopService(string) error { return nil }
