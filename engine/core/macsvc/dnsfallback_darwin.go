//go:build darwin

package macsvc

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"NekkoVPN/core/logging"
	"NekkoVPN/core/netdoctor"
)

// dnsFallback keeps names resolving on a network whose own resolver is dead
// while the network itself works. USB tethering from an iPhone does this: the
// phone's DNS forwarder (172.20.10.1) stops answering while it still routes
// packets, so apps that dial addresses (Telegram) keep working and every site
// fails with a resolve error, with the VPN off. Flushing caches and cycling
// the service bring back the same dead resolver.
//
// With the tunnel down, when the primary service takes its resolvers from
// DHCP, none of them answers and netdoctor.FallbackDNS answers through the
// same interface, the service gets the public resolvers as its manual DNS.
// A marker under dir records what was set. The setting is handed back to DHCP
// as soon as the network's own resolver answers again, and left alone once
// anyone else changed it. With the tunnel up the system resolver belongs to
// systemDNS, and nothing here runs.
type dnsFallback struct {
	dir string
	net dnsSettings
	env fallbackEnv
	// mu is systemDNS's: a decision made before the tunnel took the system
	// resolver must not overwrite the core's address. Probes run outside it.
	mu *sync.Mutex
}

// fallbackEnv is the machine side; tests stand in for it.
type fallbackEnv interface {
	// services maps network services to their devices.
	services() ([]serviceDevice, error)
	// primary is the device carrying the default route, or "".
	primary() string
	// hasIPv4: the device is up with an address, so its resolvers can be judged.
	hasIPv4(device string) bool
	// networkResolvers are the resolvers DHCP gave the device.
	networkResolvers(device string) []string
	// answers: server resolves through device.
	answers(device, server string) bool
	// tunnelHoldsDNS: systemDNS has the system resolver pointed at the core.
	tunnelHoldsDNS() bool
}

// fallbackServers is what a dead network's service gets: the resolvers the
// doctor already probes with, reachable from anywhere in RU.
var fallbackServers = netdoctor.ProbeTargets

const (
	// dnsFallbackInterval: a resolver can die without the network changing
	// (the tethering case), so the check also runs on a timer. Three queries
	// to a working resolver each time.
	dnsFallbackInterval = 2 * time.Minute
	// dnsFallbackSettle lets DHCP hand out resolvers after a network change.
	dnsFallbackSettle = 5 * time.Second
)

// Check hands back the services whose network resolver works again, then
// sets the fallback on the primary service if its own resolver is dead.
func (f dnsFallback) Check() netdoctor.Action {
	a := netdoctor.Action{Name: "dns_fallback"}
	if f.env.tunnelHoldsDNS() {
		return a
	}
	services, err := f.env.services()
	if err != nil {
		a.Detail = err.Error()
		return a
	}
	var notes []string
	released, err := f.release(services)
	if err != nil {
		notes = append(notes, err.Error())
	}
	set, err := f.apply(services)
	if err != nil {
		notes = append(notes, err.Error())
	}
	if set != "" {
		a.Done = true
		notes = append(notes, fmt.Sprintf("резервный DNS для %q", set))
	}
	for _, service := range released {
		a.Done = true
		notes = append(notes, fmt.Sprintf("DNS сети снова работает, %q возвращён на DHCP", service))
	}
	a.Detail = strings.Join(notes, "; ")
	return a
}

func (f dnsFallback) marker(service string) string {
	return filepath.Join(f.dir, fmt.Sprintf("fallback-%x", service))
}

func (f dnsFallback) release(services []serviceDevice) ([]string, error) {
	markers, _ := filepath.Glob(filepath.Join(f.dir, "fallback-*"))
	sort.Strings(markers)
	var released []string
	var errs []string
	for _, m := range markers {
		raw, err := os.ReadFile(m)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
		service, ours := lines[0], lines[1:]
		manual, err := f.net.ManualDNS(service)
		if err != nil {
			// The service is gone: nothing left to hand back.
			_ = os.Remove(m)
			continue
		}
		if !slices.Equal(manual, ours) {
			// Changed by someone else since: theirs now.
			_ = os.Remove(m)
			continue
		}
		device := deviceOf(services, service)
		if device == "" || !f.env.hasIPv4(device) {
			continue // not connected: judged when it is
		}
		if !f.anyAnswers(device, f.env.networkResolvers(device)) {
			continue
		}
		done, err := f.commit(func() (bool, error) {
			if now, err := f.net.ManualDNS(service); err != nil || !slices.Equal(now, ours) {
				return false, err
			}
			if err := f.net.SetManualDNS(service, "Empty"); err != nil {
				return false, err
			}
			_ = os.Remove(m)
			return true, nil
		})
		if err != nil {
			errs = append(errs, err.Error())
		}
		if done {
			released = append(released, service)
		}
	}
	if len(errs) > 0 {
		return released, fmt.Errorf("возврат DNS: %s", strings.Join(errs, "; "))
	}
	return released, nil
}

// apply returns the service it set the fallback on, or "".
func (f dnsFallback) apply(services []serviceDevice) (string, error) {
	device := f.env.primary()
	service := serviceOf(services, device)
	if service == "" {
		return "", nil // a VPN's own service, or no network
	}
	manual, err := f.net.ManualDNS(service)
	if err != nil {
		return "", err
	}
	if len(manual) > 0 {
		return "", nil // the user's choice, or ours already
	}
	own := f.env.networkResolvers(device)
	if len(own) == 0 || f.anyAnswers(device, own) {
		return "", nil
	}
	if !f.anyAnswers(device, fallbackServers) {
		return "", nil // offline, or a network that blocks outside DNS
	}
	done, err := f.commit(func() (bool, error) {
		if now, err := f.net.ManualDNS(service); err != nil || len(now) > 0 {
			return false, err
		}
		if err := os.MkdirAll(f.dir, 0o700); err != nil {
			return false, err
		}
		record := strings.Join(append([]string{service}, fallbackServers...), "\n") + "\n"
		if err := os.WriteFile(f.marker(service), []byte(record), 0o600); err != nil {
			return false, err
		}
		if err := f.net.SetManualDNS(service, fallbackServers...); err != nil {
			_ = os.Remove(f.marker(service))
			return false, err
		}
		return true, nil
	})
	if !done {
		return "", err
	}
	return service, nil
}

// commit changes the setting under mu, after checking again that the tunnel
// has not taken the system resolver while the probes ran.
func (f dnsFallback) commit(change func() (bool, error)) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.env.tunnelHoldsDNS() {
		return false, nil
	}
	return change()
}

func (f dnsFallback) anyAnswers(device string, servers []string) bool {
	for _, s := range servers {
		if f.env.answers(device, s) {
			return true
		}
	}
	return false
}

// watch runs Check at start, after every network change and on the timer,
// and logs what it changed.
func (f dnsFallback) watch(ctx context.Context, log *logging.Logger) {
	run := func() {
		if a := f.Check(); a.Done || a.Detail != "" {
			log.Logf("service: dns fallback: done=%v %s", a.Done, a.Detail)
		}
	}
	changed := make(chan struct{}, 1)
	go func() {
		err := netdoctor.WatchNetwork(ctx, func(string) {
			select {
			case changed <- struct{}{}:
			default:
			}
		})
		if err != nil {
			log.Logf("service: dns fallback: network watch unavailable: %v", err)
		}
	}()
	run()
	tick := time.NewTicker(dnsFallbackInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			run()
		case <-changed:
			select {
			case <-ctx.Done():
				return
			case <-time.After(dnsFallbackSettle):
			}
			run()
		}
	}
}

func deviceOf(services []serviceDevice, service string) string {
	for _, s := range services {
		if s.service == service {
			return s.device
		}
	}
	return ""
}

func serviceOf(services []serviceDevice, device string) string {
	if device == "" {
		return ""
	}
	for _, s := range services {
		if s.device == device {
			return s.service
		}
	}
	return ""
}

// macFallbackEnv is the real machine.
type macFallbackEnv struct{ dnsState string }

func (macFallbackEnv) services() ([]serviceDevice, error) {
	b, err := exec.Command("/usr/sbin/networksetup", "-listnetworkserviceorder").Output()
	if err != nil {
		return nil, fmt.Errorf("networksetup -listnetworkserviceorder: %w", err)
	}
	return parseServiceOrder(string(b)), nil
}

func (macFallbackEnv) primary() string               { return netdoctor.PrimaryInterface() }
func (macFallbackEnv) hasIPv4(device string) bool    { return deviceHasIPv4(device) }
func (macFallbackEnv) answers(device, s string) bool { return netdoctor.ResolverAnswers(device, s) }

func (macFallbackEnv) networkResolvers(device string) []string {
	b, err := exec.Command("/usr/sbin/ipconfig", "getpacket", device).Output()
	if err != nil {
		return nil
	}
	return parseDHCPResolvers(string(b))
}

func (e macFallbackEnv) tunnelHoldsDNS() bool {
	backups, _ := filepath.Glob(filepath.Join(e.dnsState, "backup-*"))
	return len(backups) > 0
}

var dhcpResolversLine = regexp.MustCompile(`(?m)^domain_name_server \([^)]*\): \{([^}]*)\}`)

// parseDHCPResolvers reads the resolvers from `ipconfig getpacket <device>`:
//
//	domain_name_server (ip_mult): {172.20.10.1, 192.168.0.1}
func parseDHCPResolvers(out string) []string {
	m := dhcpResolversLine.FindStringSubmatch(out)
	if m == nil {
		return nil
	}
	var servers []string
	for _, s := range strings.Split(m[1], ",") {
		if s = strings.TrimSpace(s); s != "" {
			servers = append(servers, s)
		}
	}
	return servers
}
