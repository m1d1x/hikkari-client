package vpn

import "errors"

// ReloadResult is the core's answer. Applied=false with Declined set means the
// config needs a restart; the running core is exactly as before.
type ReloadResult struct {
	Hash     string   `json:"hash"`
	Applied  bool     `json:"applied"`
	Declined string   `json:"declined,omitempty"`
	Error    string   `json:"error,omitempty"`
	Added    []string `json:"added,omitempty"`
	Removed  []string `json:"removed,omitempty"`
	Replaced []string `json:"replaced,omitempty"`
	Groups   []string `json:"groups,omitempty"`
}

// ErrReloadUnsupported: the running core has no control channel (an xray
// core, or a sing-box without patch 0009).
var ErrReloadUnsupported = errors.New("ядро не умеет применять конфиг на ходу")
