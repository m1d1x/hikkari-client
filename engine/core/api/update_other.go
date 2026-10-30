//go:build !windows && !darwin

package api

import (
	"errors"

	"NekkoVPN/core/svcproto"
)

// Updates are delivered by HikkariService, which exists on Windows and macOS
// only.
func (c *Core) UpdateStatus() svcproto.UpdateState {
	return svcproto.UpdateState{State: svcproto.UpdateUnavailable}
}

func (c *Core) CheckUpdate() error { return nil }

func (c *Core) startUpdates() {}

func (c *Core) RestartToUpdate() error {
	return errors.New("обновления на этой платформе пока не поддерживаются")
}
