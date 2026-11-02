//go:build darwin

package netdoctor

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/net/route"
)

const (
	reachTimeout   = 1500 * time.Millisecond
	resolveTimeout = 1000 * time.Millisecond

	scutil = "/usr/sbin/scutil"

	// iftEther is IFT_ETHER: on macOS both Ethernet and Wi-Fi report it, a
	// tunnel (utun) does not.
	iftEther = 6
	// rtfIfscope is RTF_IFSCOPE: a route that serves only sockets bound to
	// its interface, so an unscoped default route is what the system picks.
	rtfIfscope = 0x1000000
)

type macEnv struct{}

// PinsSystemResolver: the service points the system resolver at the core while
// it runs (macsvc.systemDNS), and sing-box's local resolver reads that setting.
// So every selection names the network's resolver for direct lookups, and a
// network change has to restart the tunnel to pick up the new one.
const PinsSystemResolver = true

func (macEnv) pinsResolver() bool { return PinsSystemResolver }

// SelectHost probes the machine and decides how the core's direct path
// should be set up. Call it with the tunnel down.
func SelectHost() Selection { return selectHost(macEnv{}) }

// CurrentNetwork is the identity of the physical network right now, cheap
// enough to compute on every change notification. Empty on failure.
func CurrentNetwork() string {
	routes, err := defaultRoutes()
	if err != nil {
		return ""
	}
	return networkKey(routes)
}

// defaultRoutes reads the IPv4 default routes from the kernel, one per
// interface, the route the system prefers first. The result has no resolvers
// and no interface kinds: those cost more, and only a selection needs them.
func defaultRoutes() ([]Candidate, error) {
	rib, err := route.FetchRIB(syscall.AF_INET, route.RIBTypeRoute, 0)
	if err != nil {
		return nil, fmt.Errorf("reading the routing table: %w", err)
	}
	msgs, err := route.ParseRIB(route.RIBTypeRoute, rib)
	if err != nil {
		return nil, fmt.Errorf("parsing the routing table: %w", err)
	}
	type entry struct {
		c      Candidate
		scoped bool
	}
	var entries []entry
	seen := map[int]bool{}
	for _, m := range msgs {
		rm, ok := m.(*route.RouteMessage)
		if !ok || !isDefaultRoute(rm) {
			continue
		}
		ifi, err := net.InterfaceByIndex(rm.Index)
		if err != nil || !usableInterface(ifi) {
			continue
		}
		scoped := rm.Flags&rtfIfscope != 0
		if seen[rm.Index] {
			// The unscoped route says more: it is the system's own choice.
			if !scoped {
				for i := range entries {
					if entries[i].c.Index == rm.Index {
						entries[i].scoped = false
					}
				}
			}
			continue
		}
		seen[rm.Index] = true
		entries = append(entries, entry{Candidate{Index: rm.Index, Name: ifi.Name, Gateway: gatewayOf(rm)}, scoped})
	}
	sort.SliceStable(entries, func(i, j int) bool { return !entries[i].scoped && entries[j].scoped })
	out := make([]Candidate, 0, len(entries))
	for i, e := range entries {
		e.c.Metric = uint32(i)
		out = append(out, e.c)
	}
	return out, nil
}

func isDefaultRoute(rm *route.RouteMessage) bool {
	if rm.Flags&syscall.RTF_UP == 0 || len(rm.Addrs) <= syscall.RTAX_NETMASK {
		return false
	}
	dst, ok := rm.Addrs[syscall.RTAX_DST].(*route.Inet4Addr)
	if !ok || dst.IP != [4]byte{} {
		return false
	}
	switch mask := rm.Addrs[syscall.RTAX_NETMASK].(type) {
	case nil:
		return true
	case *route.Inet4Addr:
		return mask.IP == [4]byte{}
	}
	return false
}

// gatewayOf is the next hop, or the link for a point-to-point tunnel.
func gatewayOf(rm *route.RouteMessage) string {
	if gw, ok := rm.Addrs[syscall.RTAX_GATEWAY].(*route.Inet4Addr); ok {
		return net.IP(gw.IP[:]).String()
	}
	return "link#" + strconv.Itoa(rm.Index)
}

// usableInterface: up, not loopback, and not the core's own TUN, should it
// still be up.
func usableInterface(ifi *net.Interface) bool {
	if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagRunning == 0 || ifi.Flags&net.FlagLoopback != 0 {
		return false
	}
	addrs, err := ifi.Addrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && tunNet.Contains(n.IP) {
			return false
		}
	}
	return true
}

func (macEnv) candidates() ([]Candidate, error) {
	cs, err := defaultRoutes()
	if err != nil {
		return nil, err
	}
	ether := map[int]bool{}
	if rib, err := route.FetchRIB(syscall.AF_UNSPEC, route.RIBTypeInterface, 0); err == nil {
		if msgs, err := route.ParseRIB(route.RIBTypeInterface, rib); err == nil {
			for _, m := range msgs {
				im, ok := m.(*route.InterfaceMessage)
				if !ok {
					continue
				}
				for _, sys := range im.Sys() {
					if metrics, ok := sys.(*route.InterfaceMetrics); ok && metrics.Type == iftEther {
						ether[im.Index] = true
					}
				}
			}
		}
	}
	var global []string
	var scoped map[int][]string
	if b, err := exec.Command(scutil, "--dns").Output(); err == nil {
		global, scoped = parseResolvers(string(b))
	}
	for i := range cs {
		cs[i].Physical = ether[cs[i].Index]
		cs[i].DNS = scoped[cs[i].Index]
		if len(cs[i].DNS) == 0 {
			cs[i].DNS = global
		}
	}
	return cs, nil
}

// best asks the kernel by connecting a UDP socket, which sends nothing: the
// address it picks is on the interface it would route target through.
func (macEnv) best(target string) (int, error) {
	conn, err := net.Dial("udp4", net.JoinHostPort(target, "53"))
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	local, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return 0, errors.New("no local address")
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return 0, err
	}
	for i := range ifaces {
		addrs, err := ifaces[i].Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.Equal(local.IP) {
				return ifaces[i].Index, nil
			}
		}
	}
	return 0, errors.New("no interface holds the local address")
}

// boundDialer dials through interface idx only, with IP_BOUND_IF: the core
// binds its direct sockets the same way, so a probe that passes here is what
// the core will get.
func boundDialer(idx int, timeout time.Duration) *net.Dialer {
	return &net.Dialer{
		Timeout: timeout,
		Control: func(_, _ string, c syscall.RawConn) error {
			var serr error
			err := c.Control(func(fd uintptr) {
				serr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_BOUND_IF, idx)
			})
			if err != nil {
				return err
			}
			return serr
		},
	}
}

func (macEnv) reach(idx int, target string) bool {
	conn, err := boundDialer(idx, reachTimeout).Dial("tcp4", net.JoinHostPort(target, "53"))
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (macEnv) resolve(idx int, server, domain string) bool {
	id := uint16(rand.Uint32())
	q, err := buildQuery(id, domain)
	if err != nil {
		return false
	}
	dialer := boundDialer(idx, resolveTimeout)
	if ip := net.ParseIP(server); ip != nil && ip.IsLoopback() {
		// A local stub lives on lo0: bound to the interface, the query could
		// never reach it, and a live stub would read as broken.
		dialer = &net.Dialer{Timeout: resolveTimeout}
	}
	conn, err := dialer.Dial("udp4", net.JoinHostPort(server, "53"))
	if err != nil {
		return false
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(resolveTimeout))
	if _, err := conn.Write(q); err != nil {
		return false
	}
	resp := make([]byte, 1500)
	for {
		n, err := conn.Read(resp)
		if err != nil {
			return false
		}
		// A stray datagram with another id is skipped, not judged.
		if n >= 2 && binary.BigEndian.Uint16(resp) != id {
			continue
		}
		return answered(resp[:n], id)
	}
}

func (macEnv) sleep(d time.Duration) { time.Sleep(d) }

// networkChangeMessage: the routing socket messages that can change the
// network's identity. RTM_ADD and RTM_DELETE also come for every cloned host
// route, which is why the watcher does not extend its debounce.
func networkChangeMessage(rtmType byte) bool {
	switch int(rtmType) {
	case syscall.RTM_ADD, syscall.RTM_DELETE, syscall.RTM_CHANGE, syscall.RTM_IFINFO, syscall.RTM_NEWADDR, syscall.RTM_DELADDR:
		return true
	}
	return false
}

// WatchNetwork calls onChange with the new network identity whenever the
// physical network changes (another Wi-Fi, a cable, an adapter going up or
// down), from the kernel's routing socket, which takes no rights to read.
// Notifications are batched and only a changed identity is reported: the
// core's own TUN does not change it. Runs until ctx is done.
func WatchNetwork(ctx context.Context, onChange func(network string)) error {
	fd, err := syscall.Socket(syscall.AF_ROUTE, syscall.SOCK_RAW, syscall.AF_UNSPEC)
	if err != nil {
		return fmt.Errorf("opening the routing socket: %w", err)
	}
	syscall.CloseOnExec(fd)
	// Non-blocking, so that the runtime polls it and Close ends the read.
	if err := syscall.SetNonblock(fd, true); err != nil {
		syscall.Close(fd)
		return fmt.Errorf("opening the routing socket: %w", err)
	}
	sock := os.NewFile(uintptr(fd), "routing-socket")
	defer sock.Close()

	kick := make(chan struct{}, 1)
	go func() {
		buf := make([]byte, 2048)
		for {
			n, err := sock.Read(buf)
			if err != nil {
				return
			}
			// rt_msghdr and its siblings: length, version, then the type.
			if n > 3 && networkChangeMessage(buf[3]) {
				select {
				case kick <- struct{}{}:
				default:
				}
			}
		}
	}()

	const debounce = 2 * time.Second
	last := CurrentNetwork()
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	armed := false
	for {
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-kick:
			if !armed {
				armed = true
				timer.Reset(debounce)
			}
		case <-timer.C:
			armed = false
			now := CurrentNetwork()
			if now != "" && now != last {
				last = now
				onChange(now)
			}
		}
	}
}
