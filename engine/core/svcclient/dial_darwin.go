//go:build darwin

package svcclient

import (
	"net"
	"time"

	"NekkoVPN/core/svcproto"
)

func dial(timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("unix", svcproto.SocketPath, timeout)
}
