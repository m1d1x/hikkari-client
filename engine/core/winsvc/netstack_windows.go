//go:build windows

package winsvc

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"NekkoVPN/core/logging"
	"NekkoVPN/core/netdoctor"
	"NekkoVPN/core/svcproto"
)

// netStack is the deep network reset: Winsock catalog and TCP/IP settings
// back to the Windows defaults. It undoes LSP hooks, broken Winsock entries
// and stale per-interface IP settings that third-party VPNs leave behind.
type netStack struct {
	coreDir string
	log     *logging.Logger
}

// ipAdapterDHCPEnabled is IP_ADAPTER_DHCP_ENABLED in IP_ADAPTER_ADDRESSES.Flags.
const ipAdapterDHCPEnabled = 0x4

// StaticAdapters lists the adapters that are up, are a physical NIC and
// carry an IPv4 address without DHCP. `netsh int ip reset` wipes exactly
// that configuration, which on such a machine means no network after the
// reboot until the user retypes it.
func (*netStack) StaticAdapters() ([]string, error) {
	size := uint32(15 << 10)
	var buf []byte
	for i := 0; i < 3; i++ {
		buf = make([]byte, size)
		err := windows.GetAdaptersAddresses(windows.AF_INET, windows.GAA_FLAG_SKIP_ANYCAST|windows.GAA_FLAG_SKIP_MULTICAST|windows.GAA_FLAG_SKIP_DNS_SERVER,
			0, (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])), &size)
		if err == nil {
			break
		}
		if !errors.Is(err, windows.ERROR_BUFFER_OVERFLOW) || i == 2 {
			return nil, fmt.Errorf("GetAdaptersAddresses: %w", err)
		}
	}
	var out []string
	for a := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])); a != nil; a = a.Next {
		if a.OperStatus != windows.IfOperStatusUp || a.FirstUnicastAddress == nil {
			continue
		}
		if a.IfType != windows.IF_TYPE_ETHERNET_CSMACD && a.IfType != windows.IF_TYPE_IEEE80211 {
			continue
		}
		if a.Flags&ipAdapterDHCPEnabled != 0 {
			continue
		}
		out = append(out, windows.UTF16PtrToString(a.FriendlyName))
	}
	return out, nil
}

// Reset runs both resets with netsh from System32 by absolute path: the
// service is LocalSystem and must not resolve tools through PATH. Both run
// even if the first fails, so one broken half does not keep the other from
// being repaired; any failure is reported with all the output.
//
// DPI tools go first, so the stack is rebuilt with nothing filtering it. A
// tool that would not stop does not fail the reset (the stack still needs
// its reboot); it is reported in DPIFailed. The services not being listable
// or the backup not being writable is an ordinary failure, and no tool has
// been touched then.
func (n *netStack) Reset() (svcproto.NetworkReset, error) {
	res := svcproto.NetworkReset{RebootRequired: true}
	sys, err := windows.GetSystemDirectory()
	if err != nil {
		return res, fmt.Errorf("locating System32: %w", err)
	}
	netsh := filepath.Join(sys, "netsh.exe")
	var out strings.Builder
	var failed []string

	dpi, err := netdoctor.StopDPITools(newDPISystem(n.coreDir))
	res.DPIStopped, res.DPIFailed = dpi.Stopped, dpi.Failed
	fmt.Fprintf(&out, "> dpi tools\nstopped: %s\nfailed: %s\n", strings.Join(dpi.Stopped, ", "), strings.Join(dpi.Failed, ", "))
	for _, line := range dpi.Errors {
		fmt.Fprintln(&out, line)
	}
	if err != nil {
		fmt.Fprintln(&out, err)
		failed = append(failed, fmt.Sprintf("DPI-инструменты не тронуты: %v", err))
	}

	for _, args := range [][]string{{"winsock", "reset"}, {"int", "ip", "reset"}} {
		cmd := exec.Command(netsh, args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
		b, err := cmd.CombinedOutput()
		fmt.Fprintf(&out, "> netsh %s\n%s\n", strings.Join(args, " "), strings.TrimSpace(string(b)))
		if err != nil {
			failed = append(failed, fmt.Sprintf("netsh %s: %v", strings.Join(args, " "), err))
		}
	}

	// The hosts file: entries that pin protected domains (Steam, Discord,
	// YouTube, Telegram, our own) to dead or wrong addresses are commented
	// out, after a timestamped backup beside the file. Reversible by hand
	// from the backup; the netsh reset above cannot touch it.
	if n, err := netdoctor.DisableHostsOverrides(hostsFile(sys), time.Now()); err != nil {
		fmt.Fprintf(&out, "> hosts\n%v\n", err)
		failed = append(failed, fmt.Sprintf("hosts: %v", err))
	} else {
		res.HostsDisabled = n
		fmt.Fprintf(&out, "> hosts\ndisabled %d override(s)\n", n)
	}

	res.Output = out.String()
	if len(failed) > 0 {
		// The caller gets only this message: say what did change.
		if len(res.DPIStopped) > 0 {
			failed = append(failed, "DPI-инструменты остановлены: "+strings.Join(res.DPIStopped, ", "))
		}
		return res, fmt.Errorf("сброс сетевого стека: %s", strings.Join(failed, "; "))
	}
	return res, nil
}

// hostsFile is the system hosts file.
func hostsFile(systemDir string) string {
	return filepath.Join(systemDir, "drivers", "etc", "hosts")
}

// Scan reports, read-only, what a reset would deal with and the foreign WFP
// providers nothing touches. Its parts fail on their own: a part that could
// not be read stays unknown (see svcproto.NetworkScan), the rest still
// reaches the user, and the reason goes to the service log.
func (n *netStack) Scan() (svcproto.NetworkScan, error) {
	var scan svcproto.NetworkScan
	sys, err := windows.GetSystemDirectory()
	if err != nil {
		return scan, fmt.Errorf("locating System32: %w", err)
	}
	content, err := os.ReadFile(hostsFile(sys))
	switch {
	case err == nil:
		_, scan.HostsOverrides = netdoctor.FilterHosts(content)
	case !errors.Is(err, os.ErrNotExist):
		scan.HostsOverrides = -1
		n.log.Logf("service: network scan: hosts: %v", netdoctor.BareFileError(err))
	}
	scan.DPITools, err = netdoctor.ScanDPITools(newDPISystem(n.coreDir))
	if err != nil {
		n.log.Logf("service: network scan: dpi tools: %v", err)
	}
	providers, err := wfpProviders()
	if err != nil {
		n.log.Logf("service: network scan: wfp: %v", err)
	} else {
		scan.ForeignWFP = netdoctor.ForeignWFPProviders(providers)
	}
	return scan, nil
}
