//go:build !windows && !darwin

package netdoctor

import (
	"context"
	"net"
)

// DialFunc is net.Dialer.DialContext.
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// PhysicalDialContext: no binding to the physical card here.
func PhysicalDialContext() DialFunc { return nil }
