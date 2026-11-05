package configfile

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// Fingerprint names what the written config means, to compare the config a core
// was started on with what this GUI would give it now. Read it right after a
// rebuild: admit and PinServerIPs write the file later, and a server's address
// changing between two connects is not a change of config.
//
// It leaves out what changes without a restart or is chosen anew at every connect:
// the selected profile, and the interface and resolver of the direct path.
func (s *Store) Fingerprint() (string, error) {
	cfg, err := s.Read()
	if err != nil {
		return "", err
	}
	if outbounds, ok := cfg["outbounds"].([]interface{}); ok {
		for _, it := range outbounds {
			if ob, ok := it.(map[string]interface{}); ok && ob["tag"] == SelectorTag {
				delete(ob, "default")
			}
		}
	}
	if route, ok := cfg["route"].(map[string]interface{}); ok {
		delete(route, "auto_detect_interface")
		delete(route, "default_interface")
	}
	if dns, ok := cfg["dns"].(map[string]interface{}); ok {
		if servers, ok := dns["servers"].([]interface{}); ok {
			for i, sv := range servers {
				if m, ok := sv.(map[string]interface{}); ok && m["tag"] == "dns-direct" {
					servers[i] = map[string]interface{}{"tag": "dns-direct"}
				}
			}
		}
	}
	canonical, err := json.Marshal(cfg) // Maps marshal with sorted keys.
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}
