//go:build !windows

package singbox

import "os/exec"

func hideWindow(*exec.Cmd) {}
