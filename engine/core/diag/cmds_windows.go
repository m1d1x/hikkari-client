//go:build windows

package diag

import (
	"os/exec"
	"syscall"
)

const createNoWindow = 0x08000000

func hideWindow(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}

// Output is forced to UTF-8 (chcp 65001 / [Console]::OutputEncoding),
// otherwise Russian Windows prints cp866 and the log is unreadable.
func decodeOutput(b []byte) string { return string(b) }

func cmdc(line string) command {
	return command{"cmd", []string{"/c", "chcp 65001>nul & " + line}}
}

func ps(script string) command {
	return command{"powershell", []string{"-NoProfile", "-NonInteractive", "-Command",
		"[Console]::OutputEncoding=[Text.Encoding]::UTF8; $ProgressPreference='SilentlyContinue'; " + script}}
}

const vpnProcRe = `discord|clash|mihomo|verge|v2ray|xray|sing-box|nekoray|nekobox|hiddify|amnezia|wireguard|winws|goodbyedpi|zapret|outline|happ|throne|karing|openvpn|proton|warp`

func preCommands() []command {
	return []command{
		cmdc("ver"),
		ps(`Get-CimInstance Win32_OperatingSystem | Format-List Caption,Version,BuildNumber,OSArchitecture,MUILanguages`),
		cmdc("whoami /groups | findstr /i \"S-1-16\""),
		cmdc("ipconfig /all"),
		cmdc("route print -4"),
		cmdc("netsh interface ipv4 show subinterfaces"),
		ps(`Get-NetAdapter -IncludeHidden | Format-Table -AutoSize Name,InterfaceDescription,Status,LinkSpeed,MtuSize,ifIndex | Out-String -Width 300`),
		ps(`Get-NetIPInterface -AddressFamily IPv4 | Format-Table -AutoSize ifIndex,InterfaceAlias,InterfaceMetric,NlMtu,ConnectionState,Dhcp | Out-String -Width 300`),
		ps(`Get-DnsClientServerAddress -AddressFamily IPv4 | Format-Table -AutoSize InterfaceAlias,ServerAddresses | Out-String -Width 300`),
		ps(`Get-Process | Where-Object { $_.Name -match '` + vpnProcRe + `' } | Format-Table -AutoSize Name,Id,Path | Out-String -Width 400`),
		ps(`Get-Service | Where-Object { $_.Name -match '` + vpnProcRe + `|wintun|tap' -or $_.DisplayName -match '` + vpnProcRe + `' } | Format-Table -AutoSize Name,Status,DisplayName | Out-String -Width 300`),
		cmdc("netsh winhttp show proxy"),
		// Via PowerShell: Go's argument quoting and cmd's own parsing of
		// nested quotes made `cmd /c reg query "..."` fail with "Invalid syntax".
		ps(`Get-ItemProperty 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Internet Settings' | Format-List ProxyEnable,ProxyServer,ProxyOverride,AutoConfigURL`),
		cmdc("netsh advfirewall show allprofiles state"),
		ps(`Get-MpComputerStatus | Format-List AMRunningMode,RealTimeProtectionEnabled`),
		ps(`Get-NetUDPEndpoint | Where-Object { $_.LocalAddress -ne '::' } | Group-Object OwningProcess | ForEach-Object { $p = Get-Process -Id $_.Name -ErrorAction SilentlyContinue; "{0} pid={1} udp_sockets={2} {3}" -f $p.Name,$_.Name,$_.Count,(($_.Group | Select-Object -First 6 | ForEach-Object { "$($_.LocalAddress):$($_.LocalPort)" }) -join ',') }`),
	}
}

func postCommands() []command {
	return []command{
		cmdc("ipconfig /all"),
		cmdc("route print -4"),
		cmdc("netsh interface ipv4 show subinterfaces"),
		ps(`Get-NetIPInterface -AddressFamily IPv4 | Format-Table -AutoSize ifIndex,InterfaceAlias,InterfaceMetric,NlMtu,ConnectionState | Out-String -Width 300`),
		ps(`Get-DnsClientServerAddress -AddressFamily IPv4 | Format-Table -AutoSize InterfaceAlias,ServerAddresses | Out-String -Width 300`),
		cmdc("nslookup discord.com"),
		cmdc("nslookup web.zovfy.ru"),
		ps(`Resolve-DnsName discord.media -Type A -ErrorAction SilentlyContinue | Format-Table -AutoSize Name,IPAddress | Out-String -Width 200`),
		cmdc("tracert -d -h 4 -w 800 1.1.1.1"),
		ps(`(Test-NetConnection -ComputerName 1.1.1.1 -Port 443 -WarningAction SilentlyContinue) | Format-List ComputerName,RemotePort,InterfaceAlias,SourceAddress,TcpTestSucceeded`),
	}
}

// periodicCommands show which local address Discord's UDP sockets are
// bound to — 0.0.0.0 / tun address is fine, a physical NIC IP means
// WebRTC may be sending around the tunnel.
func periodicCommands() []command {
	return []command{
		ps(`Get-NetUDPEndpoint | Where-Object { (Get-Process -Id $_.OwningProcess -ErrorAction SilentlyContinue).Name -match 'discord' } | ForEach-Object { "discord pid=$($_.OwningProcess) udp $($_.LocalAddress):$($_.LocalPort)" }`),
		ps(`Get-NetTCPConnection -State Established | Where-Object { (Get-Process -Id $_.OwningProcess -ErrorAction SilentlyContinue).Name -match 'discord' } | ForEach-Object { "discord pid=$($_.OwningProcess) tcp $($_.LocalAddress):$($_.LocalPort) -> $($_.RemoteAddress):$($_.RemotePort)" }`),
	}
}
