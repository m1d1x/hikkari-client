package singbox

import (
	"context"
	"net"

	"NekkoVPN/core/engine"
	configfile "NekkoVPN/core/engine/singbox/config"
	"NekkoVPN/core/svcproto"
)

var _ engine.HotReloader = (*Backend)(nil)

// hotReloadProcess is a core owner that can apply a config in place
// (vpn.ServiceManager, svcproto.OpReload).
type hotReloadProcess interface {
	Reload() (svcproto.ReloadOutcome, error)
}

// Reload hands what the last Configure wrote to the running core without a
// restart. The config goes through what Start does to it, admit and the
// server pins, so the core sees the same file a restart would give it: an
// unchanged profile compares equal and keeps its object and its connections.
// The pins are the ones the core runs on; a server new to it is resolved
// through the core's own direct DNS, since the system resolver answers from
// the tunnel's fake-ip range while it is up.
func (b *Backend) Reload(ctx context.Context) (engine.ReloadOutcome, error) {
	reloader, ok := b.process.(hotReloadProcess)
	if !ok {
		return engine.ReloadOutcome{Declined: "the core runs without the service"}, nil
	}
	if !b.control.IsAlive() {
		return engine.ReloadOutcome{Declined: "core is not running"}, nil
	}
	if err := b.admit(ctx); err != nil {
		return engine.ReloadOutcome{}, err
	}
	pinned := b.pins
	b.pinServers(false, func(host string) ([]net.IP, error) {
		if ip, known := pinned[host]; known {
			return []net.IP{net.ParseIP(ip)}, nil
		}
		return b.control.LookupIPv4(host)
	})
	if err := ctx.Err(); err != nil {
		return engine.ReloadOutcome{}, err
	}
	if tracked, ok := b.process.(supervisedProcess); ok {
		tracked.SetConfigHash(b.fingerprint)
	}
	outcome, err := reloader.Reload()
	if err != nil {
		return engine.ReloadOutcome{}, err
	}
	if !outcome.Applied {
		b.pins = pinned
		return engine.ReloadOutcome{Declined: outcome.Declined}, nil
	}
	// The core keeps the selection by tag; the active profile may have moved.
	b.selectActive(ctx)
	return engine.ReloadOutcome{
		Applied:  true,
		Added:    len(outcome.Added),
		Removed:  len(outcome.Removed),
		Replaced: len(outcome.Replaced),
	}, nil
}

// pinServers pins every profile server name to an IPv4 address from resolve
// (configfile.Store.PinServerIPs) and remembers what the core will dial. A
// reload logs only the pins that changed; a start logs them all.
func (b *Backend) pinServers(logAll bool, resolve configfile.Resolver) {
	results, err := b.config.PinServerIPs(resolve)
	if err != nil {
		b.log.Logf("sing-box: pin server IPs: %v", err)
	}
	pins := make(map[string]string, len(results))
	for _, r := range results {
		if r.Err != nil {
			b.log.Logf("sing-box: server %s (%s) not pinned: %v", r.Host, r.Tag, r.Err)
			continue
		}
		if logAll || b.pins[r.Host] != r.IP {
			b.log.Logf("sing-box: server %s (%s) pinned to %s", r.Host, r.Tag, r.IP)
		}
		pins[r.Host] = r.IP
	}
	b.pins = pins
}
