// Package foreignvpn ends another VPN client's live tunnel before Hikkari
// connects. Two tunnels on one machine fight over the default route and the
// system resolver, and neither works: with AmneziaVPN up on macOS its utun
// holds 0/1, the core dials the server through the physical interface and
// gets "network is unreachable" for every connection, and AmneziaVPN loses
// its internet as well (plans/2026-11-05-macos-dns-flood-core-panic.md).
//
// Only a tunnel that is up counts: a client that is open but disconnected
// changes nothing and is left alone. A client is recognized by what it runs,
// never by a window title or a folder name: its tunnel executable or its
// tunnel service. Its own app goes with the tunnel, so it does not bring the
// tunnel straight back. A background daemon without a tunnel stays: it holds
// no route, and the system would only start it again.
//
// The decision lives here and is the same on every platform; the service
// supplies the OS side (System). Runs as SYSTEM/root, in the service.
package foreignvpn

import (
	"slices"
	"strings"

	"NekkoVPN/core/svcproto"
)

// Process is one running process: its PID and its executable, a full path
// or a file name.
type Process struct {
	PID  uint32
	Path string
}

// Service is one installed Windows service.
type Service struct {
	Name    string
	Running bool
}

// SystemVPN is a VPN configuration of the OS itself (macOS: a Network
// Extension or IKEv2 service listed by `scutil --nc list`) that is
// connected or connecting. ID addresses it; Name is what the user called it.
type SystemVPN struct {
	ID   string
	Name string
}

// System is the OS side. A platform without one of the parts answers nil:
// macOS has no services, Windows no system VPN configurations this package
// manages. Errors go to the service log, so they carry no paths.
type System interface {
	Processes() ([]Process, error)
	Services() ([]Service, error)
	// StopService stops the service and waits for it; a service already
	// stopped or gone is stopped.
	StopService(name string) error
	// KillProcess ends p if its PID still runs the same executable (the PID
	// may have been reused since the listing) and waits for it to exit.
	KillProcess(p Process) error
	ConnectedVPNs() ([]SystemVPN, error)
	// StopVPN disconnects v and waits until the OS reports it disconnected.
	StopVPN(v SystemVPN) error
}

// client is one VPN product as it shows on the machine.
type client struct {
	name string
	// tunnels are the executables (lower-case file names without .exe) that
	// run only while the client's tunnel is up.
	tunnels []string
	// tunnelServices are the Windows services that carry the tunnel: an
	// exact name, or a prefix when it ends in '$' (WireGuard's
	// WireGuardTunnel$<tunnel name> scheme).
	tunnelServices []string
	// apps are the client's own executables that would reconnect: ended
	// before the tunnel.
	apps []string
}

// clients are the products recognized. A generic core (sing-box, xray,
// mihomo) is not a client: Hikkari runs one itself, and a bare core says
// nothing about whose it is.
var clients = []client{
	{
		name: "AmneziaVPN",
		// AmneziaVPN-service runs amneziawg-go for AmneziaWG and WireGuard.
		// Its proxy protocols go through a tun2socks, which other clients
		// ship too and which therefore names no client.
		tunnels:        []string{"amneziawg-go"},
		tunnelServices: []string{"AmneziaWGTunnel$"},
		apps:           []string{"amneziavpn"},
	},
	{
		name:           "WireGuard",
		tunnels:        []string{"wireguard-go"},
		tunnelServices: []string{"WireGuardTunnel$"},
		apps:           []string{"wireguard"},
	},
	{
		name:    "OpenVPN",
		tunnels: []string{"openvpn"},
		apps:    []string{"openvpn-gui"},
	},
}

// exeName is the lower-cased file name of an executable path without .exe,
// for paths of either platform.
func exeName(path string) string {
	if i := strings.LastIndexAny(path, `\/`); i >= 0 {
		path = path[i+1:]
	}
	return strings.TrimSuffix(strings.ToLower(path), ".exe")
}

func (c client) carries(service string) bool {
	for _, want := range c.tunnelServices {
		if strings.HasSuffix(want, "$") {
			// The tunnel's name follows the '$'.
			if len(service) > len(want) && strings.EqualFold(service[:len(want)], want) {
				return true
			}
			continue
		}
		if strings.EqualFold(service, want) {
			return true
		}
	}
	return false
}

// target is a client with a live tunnel and what ends it.
type target struct {
	name     string
	services []string
	apps     []Process
	tunnels  []Process
}

// targets finds the clients whose tunnel is up.
func targets(procs []Process, services []Service) []target {
	var out []target
	for _, c := range clients {
		t := target{name: c.name}
		for _, s := range services {
			if s.Running && c.carries(s.Name) {
				t.services = append(t.services, s.Name)
			}
		}
		for _, p := range procs {
			switch name := exeName(p.Path); {
			case slices.Contains(c.tunnels, name):
				t.tunnels = append(t.tunnels, p)
			case slices.Contains(c.apps, name):
				t.apps = append(t.apps, p)
			}
		}
		if len(t.services) > 0 || len(t.tunnels) > 0 {
			out = append(out, t)
		}
	}
	return out
}

// Stop ends every live foreign tunnel. A client counts as stopped only when
// all of its parts are; one failure leaves it in Failed and the others are
// still tried. logf gets each failure. The error is for a machine that could
// not be looked at: nothing was changed then.
func Stop(sys System, logf func(format string, args ...any)) (svcproto.ForeignVPNStop, error) {
	out := svcproto.ForeignVPNStop{Stopped: []string{}, Failed: []string{}}
	procs, err := sys.Processes()
	if err != nil {
		return out, err
	}
	services, err := sys.Services()
	if err != nil {
		return out, err
	}
	vpns, err := sys.ConnectedVPNs()
	if err != nil {
		return out, err
	}
	failed := func(name, part string, err error) bool {
		if err == nil {
			return false
		}
		logf("foreign vpn: %s: %s: %v", name, part, err)
		return true
	}
	for _, t := range targets(procs, services) {
		ok := true
		// The app first: left running, it sees its tunnel drop and starts
		// it again.
		for _, p := range t.apps {
			if failed(t.name, exeName(p.Path), sys.KillProcess(p)) {
				ok = false
			}
		}
		for _, name := range t.services {
			if failed(t.name, "tunnel service", sys.StopService(name)) {
				ok = false
			}
		}
		for _, p := range t.tunnels {
			if failed(t.name, exeName(p.Path), sys.KillProcess(p)) {
				ok = false
			}
		}
		record(&out, t.name, ok)
	}
	for _, v := range vpns {
		record(&out, v.Name, !failed(v.Name, "system vpn", sys.StopVPN(v)))
	}
	return out, nil
}

// record files name once, under Failed if any of its tunnels is still up.
func record(out *svcproto.ForeignVPNStop, name string, ok bool) {
	if !ok {
		out.Stopped = slices.DeleteFunc(out.Stopped, func(s string) bool { return s == name })
		if !slices.Contains(out.Failed, name) {
			out.Failed = append(out.Failed, name)
		}
		return
	}
	if !slices.Contains(out.Failed, name) && !slices.Contains(out.Stopped, name) {
		out.Stopped = append(out.Stopped, name)
	}
}
