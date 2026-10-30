//go:build !windows

package api

import (
	"errors"

	"NekkoVPN/core/svcproto"
)

// Updates are delivered by HikkariService, which exists only on Windows so
// far (docs/AUTOUPDATE-ROADMAP.md, Phase 7 for macOS).
func (c *Core) UpdateStatus() svcproto.UpdateState {
	return svcproto.UpdateState{State: svcproto.UpdateUnavailable}
}

func (c *Core) CheckUpdate() error { return nil }

func (c *Core) RestartToUpdate() error {
	return errors.New("обновления на этой платформе пока не поддерживаются")
}
