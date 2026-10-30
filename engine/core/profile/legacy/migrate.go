package legacy

import (
	"NekkoVPN/core/profile"
	"encoding/json"
	"os"
	"strings"
)

func Normalize(p profile.Profile) (profile.Profile, error) {
	if p.Connection != nil {
		return p, nil
	}
	uri, err := ExportURI(p)
	if err != nil {
		return p, err
	}
	host, port := profile.PeerHostPort(p)
	protocol, _ := p.Config["type"].(string)
	p.Connection = &profile.Connection{URI: uri, Protocol: protocol, Host: host, Port: port}
	return p, nil
}

// ReadConfig is a one-time compatibility reader, not an engine dependency.
func ReadConfig(path string) ([]profile.Profile, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(raw), "\n")
	clean := make([]string, 0, len(lines))
	for _, line := range lines {
		if !strings.HasPrefix(strings.TrimSpace(line), "//") {
			clean = append(clean, line)
		}
	}
	raw = []byte(strings.Join(clean, "\n"))
	var cfg map[string]interface{}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	eps, _ := cfg["endpoints"].([]interface{})
	if len(eps) == 0 {
		return nil, nil
	}
	ep, ok := eps[0].(map[string]interface{})
	if !ok {
		return nil, nil
	}
	return []profile.Profile{{Name: "main", Label: "Основной", Kind: profile.KindEndpoint, Config: ep}}, nil
}
func ServiceScopes(profiles []profile.Profile) map[string]string {
	scopes := make(map[string]string, len(profiles))
	for _, p := range profiles {
		scopes["p-"+p.Name] = p.Name
	}
	return scopes
}
