// Package presets owns the service catalog and user choices independently of
// manual routing rules and of the VPN backend.
package presets

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"runtime"
	"strings"

	"NekkoVPN/core/routing"
)

//go:embed defaults.json
var bundled []byte

const MaxCatalogBytes = 1024 * 1024

type Service struct {
	ID           string                `json:"id"`
	Name         string                `json:"name"`
	Icon         string                `json:"icon"`
	Domains      []string              `json:"domains"`
	IPs          []string              `json:"ips"`
	Processes    map[string][]string   `json:"processes"`
	DefaultModes []routing.RoutingMode `json:"defaultModes"`
}

type Catalog struct {
	SchemaVersion int       `json:"schemaVersion"`
	Version       int64     `json:"version"`
	Services      []Service `json:"services"`
}

var validID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// Parse rejects the entire document if any entry is invalid. Silently dropping
// a bad rule would change the promised routing behavior of a service group.
func Parse(data []byte) (Catalog, error) {
	var c Catalog
	if len(data) > MaxCatalogBytes {
		return c, fmt.Errorf("каталог превышает 1 МиБ")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&c); err != nil {
		return c, fmt.Errorf("каталог: %w", err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return c, fmt.Errorf("лишние данные после каталога")
	}
	if c.SchemaVersion != 1 || c.Version < 1 || c.Services == nil || len(c.Services) > 200 {
		return c, fmt.Errorf("неподдерживаемая схема, версия или размер каталога")
	}
	seen := map[string]bool{}
	for i := range c.Services {
		s := &c.Services[i]
		if !validID.MatchString(s.ID) || seen[s.ID] || strings.TrimSpace(s.Name) == "" || len(s.Name) > 100 || (s.Icon != "" && !validID.MatchString(s.Icon)) {
			return c, fmt.Errorf("некорректный или повторный ID/название сервиса %q", s.ID)
		}
		seen[s.ID] = true
		for _, m := range s.DefaultModes {
			if m != routing.ModeBypass && m != routing.ModeProxy {
				return c, fmt.Errorf("неизвестный режим %q", m)
			}
		}
		var err error
		if s.Domains, err = validateRules("domains", s.Domains); err != nil {
			return c, fmt.Errorf("%s: %w", s.ID, err)
		}
		if s.IPs, err = validateRules("ips", s.IPs); err != nil {
			return c, fmt.Errorf("%s: %w", s.ID, err)
		}
		for platform, entries := range s.Processes {
			if platform != "windows" && platform != "darwin" && platform != "linux" {
				return c, fmt.Errorf("неизвестная платформа %q", platform)
			}
			if s.Processes[platform], err = validateRules("processes", entries); err != nil {
				return c, fmt.Errorf("%s: %w", s.ID, err)
			}
		}
		if len(s.Domains)+len(s.IPs)+len(s.Processes["windows"])+len(s.Processes["darwin"])+len(s.Processes["linux"]) == 0 {
			return c, fmt.Errorf("пустой сервис %q", s.ID)
		}
	}
	return c, nil
}

func validateRules(category string, entries []string) ([]string, error) {
	return routing.ValidateValues(category, entries)
}

type Choices map[routing.RoutingMode]map[string]bool

func Selected(c Catalog, choices Choices, mode routing.RoutingMode, id string) bool {
	if on, exists := choices[mode][id]; exists {
		return on
	}
	for _, s := range c.Services {
		if s.ID == id {
			for _, m := range s.DefaultModes {
				if m == mode {
					return true
				}
			}
		}
	}
	return false
}

// Document preserves source provenance and all OS rules before resolution.
func Document(manual routing.RoutingState, catalog Catalog, choices Choices, platform string) routing.Document {
	d := routing.ManualDocument(manual, platform)
	for _, service := range catalog.Services {
		source := routing.Source{ID: "catalog-" + service.ID, Kind: routing.SourceCatalog, Name: service.Name}
		rules := routing.Rules{Domains: service.Domains, IPs: service.IPs, Processes: service.Processes}
		if Selected(catalog, choices, routing.ModeBypass, service.ID) {
			source.Bypass = rules
		}
		if Selected(catalog, choices, routing.ModeProxy, service.ID) {
			source.Proxy = rules
		}
		// Unselected groups belong to the catalog, not to the user's document.
		if !Selected(catalog, choices, routing.ModeBypass, service.ID) && !Selected(catalog, choices, routing.ModeProxy, service.ID) {
			continue
		}
		d.Sources = append(d.Sources, source)
	}
	return d
}

// Effective never copies service rules into manual storage.
func Effective(manual routing.RoutingState, catalog Catalog, choices Choices) (routing.RoutingState, error) {
	return Document(manual, catalog, choices, runtime.GOOS).Resolve(runtime.GOOS)
}
