//go:build windows || darwin

package api

import (
	"time"

	"NekkoVPN/core/svcclient"
	"NekkoVPN/core/svcproto"
)

// UpdateStatus is the service's view of updates. A service that cannot be
// reached reads as "unavailable", not as an error: the UI polls this, and a
// missing service already surfaces through StartVPN.
func (c *Core) UpdateStatus() svcproto.UpdateState {
	resp, err := svcclient.Call(svcproto.Request{Op: svcproto.OpStatus}, 5*time.Second)
	if err != nil || resp.Update == nil {
		return svcproto.UpdateState{State: svcproto.UpdateUnavailable}
	}
	return *resp.Update
}

// CheckUpdate asks the service to poll the channel now. The service ignores
// it if it checked within the last few minutes.
func (c *Core) CheckUpdate() error {
	_, err := svcclient.Call(svcproto.Request{Op: svcproto.OpCheckUpdate}, 5*time.Second)
	return err
}
