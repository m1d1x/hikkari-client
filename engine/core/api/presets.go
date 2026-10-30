package api

import (
	"os"

	"NekkoVPN/core/engine"
	"NekkoVPN/core/presets"
	"NekkoVPN/core/routing"
)

func presetEndpoint(name, fallback string) string {
	if value, exists := os.LookupEnv(name); exists {
		return value
	}
	return fallback
}

func (c *Core) RoutingPresets() presets.Snapshot { return c.presets.Snapshot() }
func (c *Core) SetPresetSync(on bool) error      { return c.presets.SetSync(on) }
func (c *Core) SetServicePreset(mode, id string, on bool) error {
	return c.presets.Select(routing.RoutingMode(mode), id, on)
}
func (c *Core) RefreshRoutingPresets() error { return c.presets.Refresh(c.ctx) }

func (c *Core) configureRouting(policy routing.RoutingState) error {
	pf, err := c.profiles.Read()
	if err != nil {
		return err
	}
	configuration := engine.Configuration{Profiles: pf.Profiles, ActiveProfile: pf.Active, AutoSelect: pf.Auto, Routing: policy}
	if err := engine.ValidateConfiguration(configuration, c.backend.Info().Capabilities); err != nil {
		return err
	}
	if c.routingPreview {
		return nil
	}
	if err := c.backend.Configure(configuration); err != nil {
		return err
	}
	return nil
}

func (c *Core) effectiveRouting(policy routing.RoutingState) (routing.RoutingState, error) {
	if c.presets != nil {
		s := c.presetState
		return presets.Effective(policy, s.Catalog, s.Choices)
	}
	return policy, nil
}
