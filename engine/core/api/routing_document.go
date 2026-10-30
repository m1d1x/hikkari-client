package api

import (
	"encoding/json"
	"runtime"

	"NekkoVPN/core/engine"
	"NekkoVPN/core/presets"
	"NekkoVPN/core/routing"
)

// RoutingDocument returns user intent, not native engine configuration. The
// same document can be previewed against either adapter without being applied.
func (c *Core) RoutingDocument() (routing.Document, error) {
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	manual, err := c.manualRouting()
	if err != nil {
		return routing.Document{}, err
	}
	document := routing.ManualDocument(manual, runtime.GOOS)
	if c.presets != nil {
		document = presets.Document(manual, c.presetState.Catalog, c.presetState.Choices, runtime.GOOS)
	}
	// Detach the returned value from catalog state for typed API callers too.
	raw, err := json.Marshal(document)
	if err != nil {
		return routing.Document{}, err
	}
	return routing.ParseDocument(raw)
}

type RoutingPreview struct {
	Platform  string               `json:"platform"`
	Effective routing.RoutingState `json:"effective"`
	Policy    routing.Policy       `json:"policy"`
}

// PreviewRoutingDocument is read-only. Validation errors never change files,
// live state or the selected backend. Unsupported active rules are errors.
func (c *Core) PreviewRoutingDocument(data string) (RoutingPreview, error) {
	document, err := routing.ParseDocument([]byte(data))
	if err != nil {
		return RoutingPreview{}, err
	}
	state, err := document.Resolve(runtime.GOOS)
	if err != nil {
		return RoutingPreview{}, err
	}
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	if err := engine.ValidateConfiguration(engine.Configuration{Routing: state}, c.backend.Info().Capabilities); err != nil {
		return RoutingPreview{}, err
	}
	return RoutingPreview{Platform: runtime.GOOS, Effective: state, Policy: state.Policy()}, nil
}
