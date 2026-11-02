//go:build windows || darwin

package api

import (
	"time"

	"NekkoVPN/core/svcclient"
	"NekkoVPN/core/svcproto"
)

// publishAutoTunnel pushes the autotunnel host list to the service, which
// owns the rule set file the core reads: Sanitize rewrites the config's rule
// set path to the service's directory, and OpAutoTunnel fills that file. The
// list travels as data — the GUI never names a file the service would open.
func (c *Core) publishAutoTunnel(hosts []string) error {
	_, err := svcclient.Call(svcproto.Request{Op: svcproto.OpAutoTunnel, Hosts: hosts}, 5*time.Second)
	return err
}
