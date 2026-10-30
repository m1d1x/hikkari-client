//go:build !windows

package diag

import "os/exec"

func hideWindow(*exec.Cmd) {}

func decodeOutput(b []byte) string { return string(b) }

func sh(line string) command { return command{"sh", []string{"-c", line}} }

const vpnProcRe = `discord|clash|mihomo|verge|v2ray|xray|sing-box|nekoray|hiddify|amnezia|wireguard|outline|happ|karing|openvpn|warp`

func preCommands() []command {
	return []command{
		sh("uname -a"),
		sh("sw_vers 2>/dev/null; cat /etc/os-release 2>/dev/null | head -3"),
		sh("ifconfig 2>/dev/null || ip addr"),
		sh("netstat -rn -f inet 2>/dev/null || ip route"),
		sh("scutil --dns 2>/dev/null | head -40 || cat /etc/resolv.conf"),
		sh("scutil --proxy 2>/dev/null"),
		sh("ps -axo pid,comm | grep -iE '" + vpnProcRe + "' | grep -v grep"),
	}
}

func postCommands() []command {
	return []command{
		sh("ifconfig 2>/dev/null || ip addr"),
		sh("netstat -rn -f inet 2>/dev/null || ip route"),
		sh("scutil --dns 2>/dev/null | head -40 || cat /etc/resolv.conf"),
	}
}

func periodicCommands() []command {
	return []command{
		sh("lsof -nP -iUDP 2>/dev/null | grep -i discord | head -20"),
	}
}
