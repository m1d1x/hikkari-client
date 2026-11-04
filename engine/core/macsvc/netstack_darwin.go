//go:build darwin

package macsvc

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"NekkoVPN/core/logging"
	"NekkoVPN/core/netdoctor"
	"NekkoVPN/core/svcproto"
)

const (
	hostsPath = "/etc/hosts"
	routeBin  = "/sbin/route"
	pfctl     = "/sbin/pfctl"

	// networkBackTimeout: how long a reset waits for a default route after
	// bringing the services back up. Wi-Fi association and DHCP take seconds,
	// USB tethering through an iPhone was seen to take over 20 s.
	networkBackTimeout = 60 * time.Second
)

// netStack is the doctor's privileged side on macOS. The proxies, manual
// resolvers, hosts and routes it changes belong to the whole machine, and the
// daemon decides by itself what to change: a request carries no targets.
type netStack struct {
	log *logging.Logger
	dns dnsFallback
}

// StaticAdapters: nothing here wipes a manual address. Network settings stay
// in the system's preferences and return when a service comes back up.
func (*netStack) StaticAdapters() ([]string, error) { return nil, nil }

// Repair is «починить сеть», or with proxyOnly the check before a connect.
func (n *netStack) Repair(proxyOnly bool) (svcproto.NetworkRepair, error) {
	if proxyOnly {
		a, err := netdoctor.SanitizeProxy()
		if err != nil {
			return svcproto.NetworkRepair{}, err
		}
		return svcproto.NetworkRepair{Actions: []svcproto.NetworkAction{wireAction(a)}}, nil
	}
	res, err := netdoctor.Repair()
	if err != nil {
		return svcproto.NetworkRepair{}, err
	}
	out := svcproto.NetworkRepair{Actions: make([]svcproto.NetworkAction, 0, len(res.Actions))}
	for _, a := range append(res.Actions, n.dns.Check()) {
		out.Actions = append(out.Actions, wireAction(a))
	}
	return out, nil
}

func wireAction(a netdoctor.Action) svcproto.NetworkAction {
	return svcproto.NetworkAction{Name: a.Name, Done: a.Done, Detail: a.Detail}
}

// Reset is «глубокий сброс»: the soft repair, the hosts overrides, and the
// routing table rebuilt by the system. It takes effect at once. macOS has no
// reboot-time rebuild like Windows; flushing the routes and bringing the
// active services back up makes configd set everything again from the user's
// own settings, which also clears the routes a crashed VPN left behind. Every
// part runs even if an earlier one failed.
func (n *netStack) Reset() (svcproto.NetworkReset, error) {
	var res svcproto.NetworkReset
	var out strings.Builder
	var failed []string

	repair, err := netdoctor.Repair()
	if err != nil {
		failed = append(failed, fmt.Sprintf("мягкое исправление: %v", err))
	}
	for _, a := range repair.Actions {
		fmt.Fprintf(&out, "> %s: done=%v\n", a.Name, a.Done)
	}

	hosts, err := netdoctor.DisableHostsOverrides(hostsPath, time.Now())
	if err != nil {
		failed = append(failed, fmt.Sprintf("hosts: %v", err))
	}
	res.HostsDisabled = hosts
	fmt.Fprintf(&out, "> hosts\ndisabled %d override(s)\n", hosts)

	active, err := activeDevices()
	if err != nil {
		failed = append(failed, err.Error())
	}
	b, err := exec.Command(routeBin, "-n", "flush").CombinedOutput()
	fmt.Fprintf(&out, "> route flush: %d line(s)\n", strings.Count(string(b), "\n"))
	if err != nil {
		failed = append(failed, fmt.Sprintf("route flush: %v", err))
	}
	for _, service := range active {
		if err := cycleService(service); err != nil {
			failed = append(failed, err.Error())
		}
	}
	fmt.Fprintf(&out, "> services cycled: %d\n", len(active))
	waitStart := time.Now()
	if waitForNetwork(networkBackTimeout) {
		fmt.Fprintf(&out, "> default route back after %s\n", time.Since(waitStart).Round(time.Second))
	} else {
		res.NetworkPending = true
		fmt.Fprintf(&out, "> no default route after %s\n", networkBackTimeout)
	}
	// The cycled service has its resolvers from DHCP again: judge them.
	dns := n.dns.Check()
	fmt.Fprintf(&out, "> %s: done=%v\n", dns.Name, dns.Done)

	res.Output = out.String()
	if len(failed) > 0 {
		return res, fmt.Errorf("сброс сетевого стека: %s", strings.Join(failed, "; "))
	}
	return res, nil
}

// activeDevices names the network services on hardware that has an IPv4
// address right now. A service without a device (a VPN) cannot be cycled, and
// an idle one has nothing to rebuild.
func activeDevices() ([]string, error) {
	b, err := exec.Command("/usr/sbin/networksetup", "-listnetworkserviceorder").Output()
	if err != nil {
		return nil, fmt.Errorf("networksetup -listnetworkserviceorder: %w", err)
	}
	var services []string
	for _, entry := range parseServiceOrder(string(b)) {
		if deviceHasIPv4(entry.device) {
			services = append(services, entry.service)
		}
	}
	return services, nil
}

type serviceDevice struct{ service, device string }

// parseServiceOrder reads `networksetup -listnetworkserviceorder`:
//
//	(3) Wi-Fi
//	(Hardware Port: Wi-Fi, Device: en0)
//
// A disabled service reads "(*) Name" and is left out.
func parseServiceOrder(out string) []serviceDevice {
	var entries []serviceDevice
	service := ""
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "(Hardware Port:") {
			if i := strings.LastIndex(line, "Device:"); i >= 0 && service != "" {
				device := strings.TrimSpace(strings.TrimSuffix(line[i+len("Device:"):], ")"))
				if device != "" {
					entries = append(entries, serviceDevice{service, device})
				}
			}
			service = ""
			continue
		}
		if !strings.HasPrefix(line, "(") || strings.HasPrefix(line, "(*)") {
			service = ""
			continue
		}
		if i := strings.Index(line, ") "); i > 0 {
			service = line[i+2:]
		}
	}
	return entries
}

func deviceHasIPv4(device string) bool {
	ifi, err := net.InterfaceByName(device)
	if err != nil || ifi.Flags&net.FlagUp == 0 {
		return false
	}
	addrs, err := ifi.Addrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
			return true
		}
	}
	return false
}

// cycleService turns a service off and on. Bringing it back is retried: a
// service left off is a Mac without network until the user finds the switch,
// so that failure names it.
func cycleService(service string) error {
	set := func(state string) error {
		b, err := exec.Command("/usr/sbin/networksetup", "-setnetworkserviceenabled", service, state).CombinedOutput()
		if err != nil {
			return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(b)))
		}
		return nil
	}
	if err := set("off"); err != nil {
		return fmt.Errorf("отключение сетевой службы %q: %w", service, err)
	}
	time.Sleep(time.Second)
	err := set("on")
	if err != nil {
		time.Sleep(time.Second)
		err = set("on")
	}
	if err != nil {
		return fmt.Errorf("сетевая служба %q осталась выключенной, включите её в Системных настройках: %w", service, err)
	}
	return nil
}

func waitForNetwork(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if netdoctor.CurrentNetwork() != "" {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

// Scan reports, read-only, what a reset would deal with and the foreign pf
// anchors nothing touches. A kill switch of another VPN lives in pf on macOS,
// as it does in WFP on Windows. DPITools stays unknown: the tools the deep
// reset stops are Windows drivers, and a local proxy tool shows up as a proxy.
func (n *netStack) Scan() (svcproto.NetworkScan, error) {
	var scan svcproto.NetworkScan
	content, err := os.ReadFile(hostsPath)
	switch {
	case err == nil:
		_, scan.HostsOverrides = netdoctor.FilterHosts(content)
	case !errors.Is(err, os.ErrNotExist):
		scan.HostsOverrides = -1
		n.log.Logf("service: network scan: hosts: %v", netdoctor.BareFileError(err))
	}
	anchors, err := foreignPFAnchors()
	if err != nil {
		n.log.Logf("service: network scan: pf: %v", err)
	} else {
		scan.ForeignWFP = anchors
	}
	return scan, nil
}

// foreignPFAnchors lists the anchors of an enabled pf that are not Apple's.
// With pf disabled their rules do nothing, so there is nothing to report.
func foreignPFAnchors() ([]string, error) {
	// pfctl prints its notes about ALTQ to stderr, the answer to stdout.
	info, err := exec.Command(pfctl, "-s", "info").Output()
	if err != nil {
		return nil, fmt.Errorf("pfctl -s info: %w", err)
	}
	if !strings.Contains(string(info), "Status: Enabled") {
		return []string{}, nil
	}
	b, err := exec.Command(pfctl, "-s", "Anchors").Output()
	if err != nil {
		return nil, fmt.Errorf("pfctl -s Anchors: %w", err)
	}
	return parseForeignAnchors(string(b)), nil
}

func parseForeignAnchors(out string) []string {
	anchors := []string{}
	for _, line := range strings.Split(out, "\n") {
		name := strings.TrimSpace(line)
		if name == "" || name == "com.apple" || strings.HasPrefix(name, "com.apple/") {
			continue
		}
		anchors = append(anchors, name)
	}
	return anchors
}
