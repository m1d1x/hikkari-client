//go:build windows || darwin

package netdoctor

import (
	"context"
	"errors"
	"net"
	"sort"
	"time"
)

// DialFunc is net.Dialer.DialContext.
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

const physicalDialTimeout = 10 * time.Second

type physicalEnv interface {
	candidates() ([]Candidate, error)
	// dialer dials through interface idx only.
	dialer(idx int, timeout time.Duration) *net.Dialer
}

// PhysicalDialContext dials around the tunnel: the socket is bound to the
// machine's own network card and names are resolved through that card's
// resolver, so a tunnel that swallows the request, or a fake-IP answer from
// its DNS, cannot reach it. Nil where there is no such binding.
func PhysicalDialContext() DialFunc { return physicalDial(hostEnvFor()) }

func physicalDial(env physicalEnv) DialFunc {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, err := physicalCandidate(env)
		if err != nil {
			return nil, err
		}
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ip := net.ParseIP(host)
		if ip == nil {
			if ip, err = resolveVia(ctx, env, c, host); err != nil {
				return nil, err
			}
		}
		return env.dialer(c.Index, physicalDialTimeout).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
	}
}

// physicalCandidate is the Ethernet or Wi-Fi adapter with the best metric;
// the tunnel's own adapter is never a candidate.
func physicalCandidate(env physicalEnv) (Candidate, error) {
	cs, err := env.candidates()
	if err != nil {
		return Candidate{}, err
	}
	var phys []Candidate
	for _, c := range cs {
		if c.Physical {
			phys = append(phys, c)
		}
	}
	if len(phys) == 0 {
		return Candidate{}, errors.New("no physical interface with a gateway")
	}
	sort.SliceStable(phys, func(i, j int) bool { return phys[i].Metric < phys[j].Metric })
	return phys[0], nil
}

func resolveVia(ctx context.Context, env physicalEnv, c Candidate, host string) (net.IP, error) {
	var lastErr error = errors.New("no resolver")
	for _, server := range append(append([]string{}, c.DNS...), FallbackDNS) {
		r := &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return env.dialer(c.Index, resolveTimeout).DialContext(ctx, "udp4", net.JoinHostPort(server, "53"))
			},
		}
		ips, err := r.LookupIP(ctx, "ip4", host)
		if err == nil && len(ips) > 0 {
			return ips[0], nil
		}
		if err != nil {
			lastErr = err
		}
	}
	return nil, lastErr
}
