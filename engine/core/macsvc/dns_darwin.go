//go:build darwin

package macsvc

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const networksetup = "/usr/sbin/networksetup"

// systemDNS points every network service's resolver at the core while Xray
// runs. A resolver on the local network is reached on-link, past the TUN
// routes, so without this DNS would leave unprotected — sing-box hijacks it
// by itself, Xray does not.
//
// The user's setting is backed up per service under dir before anything is
// changed, and the backup is what Restore puts back: after a clean stop, and
// at the next start of the daemon when it died with the override in place.
type systemDNS struct{ dir string }

func (d systemDNS) Override(addr string) error {
	if net.ParseIP(addr) == nil {
		return fmt.Errorf("%q is not an IP address", addr)
	}
	services, err := networkServices()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d.dir, 0o700); err != nil {
		return err
	}
	for _, service := range services {
		// Keyed by name: the list can change between two Overrides.
		backup := filepath.Join(d.dir, fmt.Sprintf("backup-%x", service))
		if _, err := os.Stat(backup); os.IsNotExist(err) {
			// Back up once: a second Override must not save its own
			// address as the user's.
			servers, err := currentServers(service)
			if err != nil {
				return err
			}
			record := strings.Join(append([]string{service}, servers...), "\n") + "\n"
			if err := os.WriteFile(backup, []byte(record), 0o600); err != nil {
				return err
			}
		}
		if err := setServers(service, addr); err != nil {
			return err
		}
	}
	return nil
}

func (d systemDNS) Restore() error {
	backups, _ := filepath.Glob(filepath.Join(d.dir, "backup-*"))
	sort.Strings(backups)
	var errs []string
	for _, backup := range backups {
		raw, err := os.ReadFile(backup)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
		servers := lines[1:]
		if len(servers) == 0 {
			// The user had none set: hand the service back to DHCP.
			servers = []string{"Empty"}
		}
		if err := setServers(lines[0], servers...); err != nil {
			errs = append(errs, err.Error())
			continue
		}
		_ = os.Remove(backup)
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	_ = os.Remove(d.dir)
	return nil
}

// networkServices lists the services networksetup knows, without the
// header line and without disabled ones (marked with *).
func networkServices() ([]string, error) {
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

// currentServers is the service's manually set resolvers; none when it
// takes them from DHCP ("There aren't any DNS Servers set on …").
func currentServers(service string) ([]string, error) {
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

func setServers(service string, servers ...string) error {
	args := append([]string{"-setdnsservers", service}, servers...)
	if out, err := exec.Command(networksetup, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("networksetup -setdnsservers %q: %v: %s", service, err, strings.TrimSpace(string(out)))
	}
	return nil
}
