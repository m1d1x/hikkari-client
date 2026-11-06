//go:build darwin

package vpn

import "os/exec"

func hideConsole(*exec.Cmd) {}
