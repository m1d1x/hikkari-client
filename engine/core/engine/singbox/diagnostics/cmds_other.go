//go:build !windows

package diag

import (
	"os/exec"
	"runtime"

	"NekkoVPN/core/svcproto"
)

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

// dnsFloodCommands look at the machine's side of a DNS flood on a Mac (tester-004,
// 04.11): the kernel's drop counters, the interface queues, where the core's own
// address routes, which apps ask mDNSResponder. Together with the core's
// [dns-diag] lines they say whether the core is asking itself and who started
// it. Temporary, remove with the fix. Empty off macOS.
func dnsFloodCommands() []command {
	if runtime.GOOS != "darwin" {
		return nil
	}
	const defaultIface = "IF=$(route -n get default 2>/dev/null | awk '/interface:/{print $2}'); "
	return []command{
		sh("netstat -s -p ip | grep -iE 'no bufs|no route|NECP|output packets sent|cantforward'"),
		sh("netstat -s -p udp | grep -iE 'no socket|full socket|dropped|datagrams output|datagrams received'"),
		sh("netstat -ibn | awk 'NR==1||$3~/Link/' | grep -E '^(Name|en[0-9]|utun|feth)'"),
		sh(defaultIface + "echo default=$IF; netstat -qq -I $IF 2>&1 | grep -E 'sched|dropped' | head -12"),
		sh(defaultIface + "for t in " + svcproto.SingBoxDNS + " 77.88.8.8; do echo unscoped; route -n get $t 2>&1 | grep -E 'interface|flags|not in'; echo scoped-$IF; route -n get -ifscope $IF $t 2>&1 | grep -E 'interface|flags|not in'; done"),
		sh("/usr/bin/log show --last 20s --style compact --predicate 'process == \"mDNSResponder\"' 2>/dev/null | grep -oE 'client pid: [0-9]+ \\([^)]*\\)' | sed -E 's/client pid: [0-9]+ //' | sort | uniq -c | sort -rn | head -10"),
		sh("ps -axo pcpu,comm -r | head -8 | sed -E 's#/[^ ]*/##'"),
		sh("ps -axo pid,comm | grep -iE 'amnezia|wireguard|openvpn|clash|mihomo|v2ray|xray|outline|warp' | grep -v grep; pgrep -l 'sing-box'"),
	}
}

func periodicCommands() []command {
	return []command{
		sh("lsof -nP -iUDP 2>/dev/null | grep -i discord | head -20"),
	}
}
