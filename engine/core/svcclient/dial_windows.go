//go:build windows

package svcclient

import (
	"net"
	"time"

	"github.com/Microsoft/go-winio"

	"NekkoVPN/core/svcproto"
)

func dial(timeout time.Duration) (net.Conn, error) {
	return winio.DialPipe(svcproto.PipeName, &timeout)
}
