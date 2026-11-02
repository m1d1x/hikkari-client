//go:build windows

package netdoctor

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ipUnicastIF is IP_UNICAST_IF: send from this socket through the given
// interface only, whatever the routing table says. The core binds its
// direct sockets the same way, so a probe that passes here is what the core
// will get.
const ipUnicastIF = 31

const (
	reachTimeout   = 1500 * time.Millisecond
	resolveTimeout = 1000 * time.Millisecond
)

type winEnv struct{}

// PinsSystemResolver: strict_route catches DNS on Windows, so the system
// setting stays the user's and sing-box's local resolver can read it.
const PinsSystemResolver = false

func (winEnv) pinsResolver() bool { return PinsSystemResolver }

// SelectHost probes the machine and decides how the core's direct path
// should be set up. Call it with the tunnel down.
func SelectHost() Selection { return selectHost(winEnv{}) }

// CurrentNetwork is the identity of the physical network right now, cheap
// enough to compute on every change notification. Empty on failure.
func CurrentNetwork() string {
	cs, err := winEnv{}.candidates()
	if err != nil {
		return ""
	}
	return networkKey(cs)
}

func adapters() ([]byte, error) {
	size := uint32(15 << 10)
	for i := 0; i < 3; i++ {
		buf := make([]byte, size)
		err := windows.GetAdaptersAddresses(windows.AF_INET,
			windows.GAA_FLAG_INCLUDE_GATEWAYS|windows.GAA_FLAG_SKIP_ANYCAST|windows.GAA_FLAG_SKIP_MULTICAST,
			0, (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])), &size)
		if err == nil {
			return buf, nil
		}
		if !errors.Is(err, windows.ERROR_BUFFER_OVERFLOW) {
			return nil, fmt.Errorf("GetAdaptersAddresses: %w", err)
		}
	}
	return nil, errors.New("GetAdaptersAddresses: buffer keeps growing")
}

func (winEnv) candidates() ([]Candidate, error) {
	buf, err := adapters()
	if err != nil {
		return nil, err
	}
	var out []Candidate
	for a := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])); a != nil; a = a.Next {
		if a.OperStatus != windows.IfOperStatusUp || a.IfType == windows.IF_TYPE_SOFTWARE_LOOPBACK {
			continue
		}
		var gw net.IP
		for g := a.FirstGatewayAddress; g != nil; g = g.Next {
			if ip := g.Address.IP().To4(); ip != nil && !ip.IsUnspecified() {
				gw = ip
				break
			}
		}
		if gw == nil {
			continue
		}
		ours := false
		for u := a.FirstUnicastAddress; u != nil; u = u.Next {
			if ip := u.Address.IP(); ip != nil && tunNet.Contains(ip) {
				ours = true
			}
		}
		if ours {
			continue
		}
		c := Candidate{Index: int(a.IfIndex), Name: windows.UTF16PtrToString(a.FriendlyName), Metric: a.Ipv4Metric, Gateway: gw.String(),
			Physical: a.IfType == windows.IF_TYPE_ETHERNET_CSMACD || a.IfType == windows.IF_TYPE_IEEE80211}
		for d := a.FirstDnsServerAddress; d != nil; d = d.Next {
			if ip := d.Address.IP().To4(); ip != nil {
				c.DNS = append(c.DNS, ip.String())
			}
		}
		out = append(out, c)
	}
	return out, nil
}

func (winEnv) best(target string) (int, error) {
	ip := net.ParseIP(target).To4()
	if ip == nil {
		return 0, fmt.Errorf("not an IPv4 address: %q", target)
	}
	sa := &windows.SockaddrInet4{}
	copy(sa.Addr[:], ip)
	var idx uint32
	if err := windows.GetBestInterfaceEx(sa, &idx); err != nil {
		return 0, err
	}
	return int(idx), nil
}

// boundDialer dials through interface idx only.
func boundDialer(idx int, timeout time.Duration) *net.Dialer {
	return &net.Dialer{
		Timeout: timeout,
		Control: func(_, _ string, c syscall.RawConn) error {
			var serr error
			err := c.Control(func(fd uintptr) {
				// IP_UNICAST_IF takes the index in network byte order.
				var be [4]byte
				binary.BigEndian.PutUint32(be[:], uint32(idx))
				serr = windows.SetsockoptInt(windows.Handle(fd), windows.IPPROTO_IP, ipUnicastIF, int(binary.LittleEndian.Uint32(be[:])))
			})
			if err != nil {
				return err
			}
			return serr
		},
	}
}

func (winEnv) reach(idx int, target string) bool {
	conn, err := boundDialer(idx, reachTimeout).Dial("tcp4", net.JoinHostPort(target, "53"))
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (winEnv) resolve(idx int, server, domain string) bool {
	id := uint16(rand.Uint32())
	q, err := buildQuery(id, domain)
	if err != nil {
		return false
	}
	conn, err := boundDialer(idx, resolveTimeout).Dial("udp4", net.JoinHostPort(server, "53"))
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

func (winEnv) sleep(d time.Duration) { time.Sleep(d) }

// WatchNetwork calls onChange with the new network identity whenever the
// physical network changes (another Wi-Fi, a cable, an adapter going up or
// down). Notifications come in bursts, so they are debounced, and only a
// changed identity is reported: the core's own TUN has no gateway and does
// not change it. Runs until ctx is done.
func WatchNetwork(ctx context.Context, onChange func(network string)) error {
	kick := make(chan struct{}, 1)
	w := &watcher{kick: kick}
	watchersMu.Lock()
	id := nextWatcher
	nextWatcher++
	watchers[id] = w
	watchersMu.Unlock()
	defer func() {
		watchersMu.Lock()
		delete(watchers, id)
		watchersMu.Unlock()
	}()

	var hIface, hAddr windows.Handle
	if err := windows.NotifyIpInterfaceChange(windows.AF_INET, notifyCallback, nil, false, &hIface); err != nil {
		return fmt.Errorf("NotifyIpInterfaceChange: %w", err)
	}
	defer windows.CancelMibChangeNotify2(hIface)
	if err := windows.NotifyUnicastIpAddressChange(windows.AF_INET, notifyCallback, nil, false, &hAddr); err != nil {
		return fmt.Errorf("NotifyUnicastIpAddressChange: %w", err)
	}
	defer windows.CancelMibChangeNotify2(hAddr)

	const debounce = 2 * time.Second
	last := CurrentNetwork()
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	for {
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-kick:
			timer.Reset(debounce)
		case <-timer.C:
			now := CurrentNetwork()
			if now != "" && now != last {
				last = now
				onChange(now)
			}
		}
	}
}

type watcher struct{ kick chan struct{} }

// The OS calls back on its own thread. No Go pointer may travel through the
// callback context, so every live watcher is kicked from a registry; there
// is one per engine in practice.
var (
	watchersMu  sync.Mutex
	watchers            = map[uintptr]*watcher{}
	nextWatcher uintptr = 1

	notifyCallback = windows.NewCallback(func(_, _, _ uintptr) uintptr {
		watchersMu.Lock()
		defer watchersMu.Unlock()
		for _, w := range watchers {
			select {
			case w.kick <- struct{}{}:
			default:
			}
		}
		return 0
	})
)

func (winEnv) dialer(idx int, timeout time.Duration) *net.Dialer { return boundDialer(idx, timeout) }

func hostEnvFor() physicalEnv { return winEnv{} }
