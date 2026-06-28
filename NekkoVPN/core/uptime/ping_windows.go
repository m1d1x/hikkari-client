//go:build windows

package uptime

import (
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var rttRe = regexp.MustCompile(`(?i)time[=<]\s*(\d+)\s*ms`)

func pingHost(host string) (bool, int) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(PingTimeoutMs+300)*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ping", "-n", "1", "-w", strconv.Itoa(PingTimeoutMs), host)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	s := string(out)
	low := strings.ToLower(s)
	if strings.Contains(low, "request timed out") ||
		strings.Contains(low, "could not find host") ||
		strings.Contains(low, "destination host unreachable") {
		return false, 0
	}
	if err != nil {
		return false, 0
	}
	m := rttRe.FindStringSubmatch(s)
	if m == nil {
		return false, 0
	}
	rtt, _ := strconv.Atoi(m[1])
	return true, rtt
}
