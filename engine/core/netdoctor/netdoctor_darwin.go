//go:build darwin

package netdoctor

import (
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"sync"
)

const (
	networksetup = "/usr/sbin/networksetup"
	dscacheutil  = "/usr/bin/dscacheutil"
	killall      = "/usr/bin/killall"
)

// The proxy kinds macOS has per network service, with the networksetup flags
// that read them and switch them.
var proxyKinds = []struct{ name, get, setState string }{
	{"web", "-getwebproxy", "-setwebproxystate"},
	{"secure", "-getsecurewebproxy", "-setsecurewebproxystate"},
	{"socks", "-getsocksfirewallproxy", "-setsocksfirewallproxystate"},
}

// NetworkServices lists the services networksetup knows, without the header
// line and without disabled ones (marked with *).
func NetworkServices() ([]string, error) {
	out, err := exec.Command(networksetup, "-listallnetworkservices").Output()
	if err != nil {
		return nil, fmt.Errorf("networksetup -listallnetworkservices: %w", err)
	}
	var services []string
	for i, line := range strings.Split(string(out), "\n") {
		if i == 0 || line == "" || strings.HasPrefix(line, "*") {
			continue
		}
		services = append(services, line)
	}
	return services, nil
}

// ManualDNS is the service's manually set resolvers; none when it takes them
// from DHCP ("There aren't any DNS Servers set on …").
func ManualDNS(service string) ([]string, error) {
	out, err := exec.Command(networksetup, "-getdnsservers", service).Output()
	if err != nil {
		return nil, fmt.Errorf("networksetup -getdnsservers %q: %w", service, err)
	}
	var servers []string
	for _, line := range strings.Split(string(out), "\n") {
		if ip := strings.TrimSpace(line); net.ParseIP(ip) != nil {
			servers = append(servers, ip)
		}
	}
	return servers, nil
}

// SetManualDNS replaces the service's manual resolvers; "Empty" hands the
// service back to DHCP. Needs root.
func SetManualDNS(service string, servers ...string) error {
	args := append([]string{"-setdnsservers", service}, servers...)
	if out, err := exec.Command(networksetup, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("networksetup -setdnsservers %q: %v: %s", service, err, strings.TrimSpace(string(out)))
	}
	return nil
}

type macProxies struct{}

// list reads concurrently: each read is a process of about 60 ms, and it runs
// before every connect, on a machine with any number of services.
func (macProxies) list() ([]ServiceProxy, error) {
	services, err := NetworkServices()
	if err != nil {
		return nil, err
	}
	out := make([]ServiceProxy, len(services)*len(proxyKinds))
	errs := make([]error, len(out))
	var wg sync.WaitGroup
	for i, service := range services {
		for j, kind := range proxyKinds {
			wg.Add(1)
			go func(slot int, service, name, flag string) {
				defer wg.Done()
				b, err := exec.Command(networksetup, flag, service).Output()
				if err != nil {
					errs[slot] = fmt.Errorf("networksetup %s %q: %w", flag, service, err)
					return
				}
				out[slot] = ServiceProxy{Service: service, Kind: name, Setting: parseServiceProxy(string(b))}
			}(i*len(proxyKinds)+j, service, kind.name, kind.get)
		}
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return out, nil
}

func (macProxies) disable(service, kind string) error {
	for _, k := range proxyKinds {
		if k.name != kind {
			continue
		}
		if b, err := exec.Command(networksetup, k.setState, service, "off").CombinedOutput(); err != nil {
			return fmt.Errorf("networksetup %s %q: %v: %s", k.setState, service, err, strings.TrimSpace(string(b)))
		}
		return nil
	}
	return fmt.Errorf("unknown proxy kind %q", kind)
}

// Diagnose inspects the machine's network settings without changing anything.
func Diagnose() (Report, error) {
	list, err := macProxies{}.list()
	if err != nil {
		return Report{}, err
	}
	report, _ := classifyByService(list)
	return Report{Supported: true, Proxy: report}, nil
}

// SanitizeProxy disables the system proxy of the network services where it is
// orphaned (see Classify). Needs root: the service runs it, on the GUI's
// request before a connect.
func SanitizeProxy() (Action, error) { return repairProxies(macProxies{}) }

// resetDeadDNS hands back to DHCP the services whose manual resolvers are a
// local stub that no longer runs. Everything on such a machine fails to
// resolve, and the override the core sets while Xray runs is never dead while
// it is in place: the core answers on it.
func resetDeadDNS() Action {
	a := Action{Name: "dns_reset"}
	services, err := NetworkServices()
	if err != nil {
		a.Detail = err.Error()
		return a
	}
	var failed []string
	for _, service := range services {
		servers, err := ManualDNS(service)
		if err != nil {
			failed = append(failed, err.Error())
			continue
		}
		if !deadLoopbackDNS(servers, dnsStubAlive) {
			continue
		}
		if err := SetManualDNS(service, "Empty"); err != nil {
			failed = append(failed, err.Error())
			continue
		}
		a.Done = true
	}
	if len(failed) > 0 {
		a.Detail = strings.Join(failed, "; ")
	}
	return a
}

// flushDNS empties the resolver caches, as Apple documents it: the directory
// service's and mDNSResponder's. Both are tried; needs root.
func flushDNS() error {
	var errs []error
	if out, err := exec.Command(dscacheutil, "-flushcache").CombinedOutput(); err != nil {
		errs = append(errs, fmt.Errorf("dscacheutil: %v: %s", err, strings.TrimSpace(string(out))))
	}
	if out, err := exec.Command(killall, "-HUP", "mDNSResponder").CombinedOutput(); err != nil {
		errs = append(errs, fmt.Errorf("mDNSResponder: %v: %s", err, strings.TrimSpace(string(out))))
	}
	return errors.Join(errs...)
}

// Repair is the soft repair: orphaned proxies off, dead local resolvers back
// to DHCP, DNS caches flushed. Needs root and no reboot.
func Repair() (RepairResult, error) {
	before, err := Diagnose()
	if err != nil {
		return RepairResult{}, err
	}
	res := RepairResult{Before: before}
	proxy, err := SanitizeProxy()
	if err != nil {
		proxy.Detail = err.Error()
	}
	res.Actions = append(res.Actions, proxy, resetDeadDNS())
	flush := Action{Name: "dns_flush"}
	if err := flushDNS(); err != nil {
		flush.Detail = err.Error()
	} else {
		flush.Done = true
	}
	res.Actions = append(res.Actions, flush)
	return res, nil
}
