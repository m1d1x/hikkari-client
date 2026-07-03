// JSON dispatch for FFI. Call(method, argsJSON) routes to a typed method
// and always returns a result wrapped in an envelope:
//
//	{"ok": true, "data": <whatever>}
//	{"ok": false, "error": "human message"}
//
// This is the single C function we'll export from cgo for dart:ffi on the
// desktop side. On mobile we don't strictly need it (gomobile exports typed
// methods directly), but using the same envelope keeps the Flutter client
// code identical across platforms.

package api

import (
	"encoding/json"
	"fmt"
)

// Envelope is the universal result wrapper. Exactly one of Data / Error is
// set, mirrored by Ok.
type Envelope struct {
	Ok    bool            `json:"ok"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error string          `json:"error,omitempty"`
}

// Call dispatches a method by name. argsJSON is a JSON object whose fields
// match the method's named parameters; pass "" or "null" for no-arg methods.
// The return value is always a marshalled Envelope — Call itself never
// returns an error so callers across the FFI boundary have one code path.
func (c *Core) Call(method, argsJSON string) string {
	data, err := c.dispatch(method, argsJSON)
	if err != nil {
		return marshalEnvelope(Envelope{Ok: false, Error: err.Error()})
	}
	raw, mErr := json.Marshal(data)
	if mErr != nil {
		return marshalEnvelope(Envelope{
			Ok:    false,
			Error: fmt.Sprintf("api.Core: marshal result: %v", mErr),
		})
	}
	return marshalEnvelope(Envelope{Ok: true, Data: raw})
}

// dispatch routes the call. Each case is intentionally explicit — easier to
// audit than reflection-based routing, and the FFI surface is small enough
// to stay readable.
func (c *Core) dispatch(method, argsJSON string) (interface{}, error) {
	switch method {

	// ----- lifecycle -----
	case "Init":
		var a struct {
			BaseDir string `json:"baseDir"`
		}
		if err := decodeArgs(argsJSON, &a); err != nil {
			return nil, err
		}
		return nil, c.Init(a.BaseDir)
	case "Shutdown":
		return nil, c.Shutdown()

	// ----- VPN control -----
	case "StartVPN":
		return nil, c.StartVPN()
	case "StopVPN":
		return nil, c.StopVPN()
	case "RestartVPN":
		return nil, c.RestartVPN()
	case "IsRunning":
		return c.IsRunning(), nil

	// ----- health -----
	case "Health":
		return c.Health(), nil
	case "ExternalIP":
		return c.ExternalIP(), nil

	// ----- logging -----
	case "LogTail":
		return c.LogTail(), nil

	// ----- routing -----
	case "Routing":
		return c.Routing()
	case "SetRoutingEnabled":
		var a struct {
			On bool `json:"on"`
		}
		if err := decodeArgs(argsJSON, &a); err != nil {
			return nil, err
		}
		return nil, c.SetRoutingEnabled(a.On)
	case "SetRoutingMode":
		var a struct {
			Mode string `json:"mode"`
		}
		if err := decodeArgs(argsJSON, &a); err != nil {
			return nil, err
		}
		return nil, c.SetRoutingMode(a.Mode)
	case "AddRule":
		var a struct {
			Mode     string `json:"mode"`
			Category string `json:"category"`
			Value    string `json:"value"`
		}
		if err := decodeArgs(argsJSON, &a); err != nil {
			return nil, err
		}
		return nil, c.AddRule(a.Mode, a.Category, a.Value)
	case "RemoveRule":
		var a struct {
			Mode     string `json:"mode"`
			Category string `json:"category"`
			Value    string `json:"value"`
		}
		if err := decodeArgs(argsJSON, &a); err != nil {
			return nil, err
		}
		return nil, c.RemoveRule(a.Mode, a.Category, a.Value)

	// ----- profiles -----
	case "Profiles":
		return c.Profiles()
	case "ActiveProfile":
		return c.ActiveProfile()
	case "RenameProfile":
		var a struct {
			Name  string `json:"name"`
			Label string `json:"label"`
		}
		if err := decodeArgs(argsJSON, &a); err != nil {
			return nil, err
		}
		return nil, c.RenameProfile(a.Name, a.Label)
	case "DeleteProfile":
		var a struct {
			Name string `json:"name"`
		}
		if err := decodeArgs(argsJSON, &a); err != nil {
			return nil, err
		}
		return nil, c.DeleteProfile(a.Name)
	case "SwitchProfile":
		var a struct {
			Name string `json:"name"`
		}
		if err := decodeArgs(argsJSON, &a); err != nil {
			return nil, err
		}
		return nil, c.SwitchProfile(a.Name)
	case "AddProfileFromURI":
		var a struct {
			URI string `json:"uri"`
		}
		if err := decodeArgs(argsJSON, &a); err != nil {
			return nil, err
		}
		return c.AddProfileFromURI(a.URI)
	case "ProfileCountries":
		return c.ProfileCountries()
	case "AutoMode":
		return c.AutoMode()
	case "SetAutoMode":
		var a struct {
			On bool `json:"on"`
		}
		if err := decodeArgs(argsJSON, &a); err != nil {
			return nil, err
		}
		return nil, c.SetAutoMode(a.On)

	// ----- uptime -----
	case "UptimeHistory":
		return c.UptimeHistory(), nil

	// ----- service uptime -----
	case "PingServices":
		var a struct {
			ActiveTag string              `json:"activeTag"`
			Services  []ServicePingTarget `json:"services"`
		}
		if err := decodeArgs(argsJSON, &a); err != nil {
			return nil, err
		}
		return c.PingServices(a.ActiveTag, a.Services), nil
	case "ServiceUptimeHistory":
		return c.ServiceUptimeHistory(), nil

	// ----- custom services -----
	case "ListCustomServices":
		return c.ListCustomServices(), nil
	case "AddCustomService":
		var a struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		}
		if err := decodeArgs(argsJSON, &a); err != nil {
			return nil, err
		}
		return nil, c.AddCustomService(a.Name, a.URL)
	case "RemoveCustomService":
		var a struct {
			Name string `json:"name"`
		}
		if err := decodeArgs(argsJSON, &a); err != nil {
			return nil, err
		}
		return nil, c.RemoveCustomService(a.Name)
	// ----- app discovery -----
	case "ListApps":
		var a struct {
			All bool `json:"all"`
		}
		_ = decodeArgs(argsJSON, &a)
		return c.ListApps(a.All), nil
	}
	return nil, fmt.Errorf("api.Core: unknown method %q", method)
}

// decodeArgs unmarshals argsJSON into dst. Empty / "null" args are tolerated
// so callers can send "" for no-arg methods even when one is expected.
func decodeArgs(argsJSON string, dst interface{}) error {
	if argsJSON == "" || argsJSON == "null" {
		return nil
	}
	if err := json.Unmarshal([]byte(argsJSON), dst); err != nil {
		return fmt.Errorf("api.Core: bad args JSON: %w", err)
	}
	return nil
}

func marshalEnvelope(e Envelope) string {
	// Marshal of a struct with these field types never fails — but if it
	// ever did, returning a hand-built fallback keeps the FFI contract.
	raw, err := json.Marshal(e)
	if err != nil {
		return `{"ok":false,"error":"api.Core: failed to marshal envelope"}`
	}
	return string(raw)
}
