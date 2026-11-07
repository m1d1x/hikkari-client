//go:build windows || darwin

package api

import (
	"errors"
	"time"

	"NekkoVPN/core/svcclient"
	"NekkoVPN/core/svcproto"
)

// foreignVPNStopTimeout covers the service's worst case: a client's app and
// tunnel each given SIGTERM and then SIGKILL, a tunnel service stopping, a
// system VPN disconnecting.
const foreignVPNStopTimeout = 45 * time.Second

// stopForeignVPNInService asks the service to end another VPN client's tunnel before
// a connect and returns the clients it ended. It never blocks the connect:
// a service from before the op, or one that could not look, is only logged.
func (c *Core) stopForeignVPNInService() []string {
	resp, err := svcclient.Call(svcproto.Request{Op: svcproto.OpStopForeignVPN}, foreignVPNStopTimeout)
	if err != nil {
		// No service at all surfaces through the start itself.
		if c.logger != nil && !errors.Is(err, svcclient.ErrNoService) {
			c.logger.Logf("[foreignvpn] %v", err)
		}
		return nil
	}
	if resp.ForeignVPN == nil {
		return nil
	}
	stop := *resp.ForeignVPN
	if c.logger != nil && len(stop.Stopped)+len(stop.Failed) > 0 {
		c.logger.Logf("[foreignvpn] stopped=%s failed=%s", svcproto.ScanList(stop.Stopped), svcproto.ScanList(stop.Failed))
	}
	return stop.Stopped
}
