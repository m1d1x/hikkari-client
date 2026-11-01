//go:build darwin

package macsvc

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"NekkoVPN/core/netdoctor"
)

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
	services, err := netdoctor.NetworkServices()
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
			servers, err := netdoctor.ManualDNS(service)
			if err != nil {
				return err
			}
			record := strings.Join(append([]string{service}, servers...), "\n") + "\n"
			if err := os.WriteFile(backup, []byte(record), 0o600); err != nil {
				return err
			}
		}
		if err := netdoctor.SetManualDNS(service, addr); err != nil {
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
		if err := netdoctor.SetManualDNS(lines[0], servers...); err != nil {
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
