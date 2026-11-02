package diag

import (
	"encoding/json"
	"testing"
)

func TestParseProbeTargets(t *testing.T) {
	if got, err := parseProbeTargets(json.RawMessage(`{"targets":[{"label":"ams7-443","addr":"89.124.99.31:443"}]}`)); err != nil || len(got) != 1 {
		t.Fatalf("good target: %v, %v", got, err)
	}
	for name, raw := range map[string]string{
		"no params": `{}`,
		"private":   `{"targets":[{"label":"a","addr":"10.0.0.1:443"}]}`,
		"loopback":  `{"targets":[{"label":"a","addr":"127.0.0.1:9090"}]}`,
		"fakeip":    `{"targets":[{"label":"a","addr":"198.18.0.1:443"}]}`,
		"hostname":  `{"targets":[{"label":"a","addr":"example.org:443"}]}`,
		"label":     `{"targets":[{"label":"1.2.3.4","addr":"1.2.3.4:443"}]}`,
	} {
		if _, err := parseProbeTargets(json.RawMessage(raw)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
