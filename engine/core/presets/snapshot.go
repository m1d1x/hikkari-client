package presets

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"NekkoVPN/core/routing"
)

type RoutingSnapshot struct {
	Version   int               `json:"version"`
	CreatedAt int64             `json:"created_at"`
	Mode      string            `json:"mode,omitempty"`
	Groups    map[string]string `json:"groups"`
	Services  map[string]string `json:"services"`

	// parsedCategories retains explicit category if provided in categorized services map
	parsedCategories map[string]string `json:"-"`
}

// Bundled returns the raw defaults.json bytes embedded in the binary.
func Bundled() []byte {
	return bundled
}

// BundledCatalog parses and returns the bundled defaults.json catalog.
func BundledCatalog() (Catalog, error) {
	return Parse(bundled)
}

// Clone returns a deep copy of a State.
func Clone(s State) State {
	return clone(s)
}

func hasExecExtension(s string) bool {
	lower := strings.ToLower(s)
	return strings.HasSuffix(lower, ".exe")
}

func isValidAction(a string) bool {
	return a == "proxy" || a == "bypass" || a == "block"
}

// ParseRoutingSnapshot parses and validates a JSON routing snapshot according to schema v1.
func ParseRoutingSnapshot(data []byte) (*RoutingSnapshot, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, fmt.Errorf("пустые данные для импорта")
	}

	var raw struct {
		Version   *int            `json:"version"`
		CreatedAt *int64          `json:"created_at"`
		Mode      *string         `json:"mode"`
		Groups    json.RawMessage `json:"groups"`
		Services  json.RawMessage `json:"services"`
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		var loose struct {
			Version   *int            `json:"version"`
			CreatedAt *int64          `json:"created_at"`
			Mode      *string         `json:"mode"`
			Groups    json.RawMessage `json:"groups"`
			Services  json.RawMessage `json:"services"`
		}
		if looseErr := json.Unmarshal(data, &loose); looseErr != nil {
			return nil, fmt.Errorf("некорректный JSON: %w", looseErr)
		}
		raw = loose
	}

	if raw.Version == nil {
		return nil, fmt.Errorf("отсутствует обязательное поле \"version\"")
	}
	if *raw.Version != 1 {
		return nil, fmt.Errorf("неподдерживаемая версия маршрутизации: %d (поддерживается только версия 1)", *raw.Version)
	}

	snapshot := &RoutingSnapshot{
		Version:          *raw.Version,
		Groups:           make(map[string]string),
		Services:         make(map[string]string),
		parsedCategories: make(map[string]string),
	}

	if raw.CreatedAt != nil {
		snapshot.CreatedAt = *raw.CreatedAt
	}

	if raw.Mode != nil && *raw.Mode != "" {
		m := strings.ToLower(strings.TrimSpace(*raw.Mode))
		if m != string(routing.ModeBypass) && m != string(routing.ModeProxy) {
			return nil, fmt.Errorf("неизвестный режим маршрутизации %q (разрешены только bypass и proxy)", *raw.Mode)
		}
		snapshot.Mode = m
	}

	// Validate groups
	if len(raw.Groups) > 0 && !bytes.Equal(bytes.TrimSpace(raw.Groups), []byte("null")) {
		var groupsMap map[string]any
		if err := json.Unmarshal(raw.Groups, &groupsMap); err != nil {
			return nil, fmt.Errorf("поле \"groups\" должно быть объектом")
		}
		for id, v := range groupsMap {
			valStr, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("недопустимый тип значения для группы %q: ожидается строка", id)
			}
			valStr = strings.ToLower(strings.TrimSpace(valStr))
			if !isValidAction(valStr) {
				return nil, fmt.Errorf("недопустимое значение для группы %q: %q (разрешены только proxy, bypass, block)", id, valStr)
			}
			snapshot.Groups[id] = valStr
		}
	}

	// Validate services
	if len(raw.Services) > 0 && !bytes.Equal(bytes.TrimSpace(raw.Services), []byte("null")) {
		var servicesMap map[string]any
		if err := json.Unmarshal(raw.Services, &servicesMap); err != nil {
			return nil, fmt.Errorf("поле \"services\" должно быть объектом")
		}
		for key, v := range servicesMap {
			if valStr, ok := v.(string); ok {
				valStr = strings.ToLower(strings.TrimSpace(valStr))
				if !isValidAction(valStr) {
					return nil, fmt.Errorf("недопустимое значение для правила %q: %q (разрешены только proxy, bypass, block)", key, valStr)
				}
				snapshot.Services[key] = valStr
			} else if subMap, ok := v.(map[string]any); ok {
				// Categorized sub-map e.g. "domains": {"example.com": "proxy"}
				catName := strings.ToLower(strings.TrimSpace(key))
				for subKey, subVal := range subMap {
					subValStr, ok := subVal.(string)
					if !ok {
						return nil, fmt.Errorf("недопустимый тип значения для правила %q.%q: ожидается строка", key, subKey)
					}
					subValStr = strings.ToLower(strings.TrimSpace(subValStr))
					if !isValidAction(subValStr) {
						return nil, fmt.Errorf("недопустимое значение для правила %q.%q: %q (разрешены только proxy, bypass, block)", key, subKey, subValStr)
					}
					snapshot.Services[subKey] = subValStr
					snapshot.parsedCategories[subKey] = catName
				}
			} else {
				return nil, fmt.Errorf("недопустимый тип значения для правила %q", key)
			}
		}
	}

	return snapshot, nil
}

// BuildRoutingSnapshot constructs a complete snapshot from current routing state, catalog, and choices.
func BuildRoutingSnapshot(manual routing.RoutingState, catalog Catalog, choices Choices) RoutingSnapshot {
	snapshot := RoutingSnapshot{
		Version:   1,
		CreatedAt: time.Now().Unix(),
		Mode:      string(manual.Mode),
		Groups:    make(map[string]string),
		Services:  make(map[string]string),
	}

	for _, s := range catalog.Services {
		if Selected(catalog, choices, routing.ModeProxy, s.ID) {
			snapshot.Groups[s.ID] = "proxy"
		} else if Selected(catalog, choices, routing.ModeBypass, s.ID) {
			snapshot.Groups[s.ID] = "bypass"
		} else {
			snapshot.Groups[s.ID] = "block"
		}
	}

	// Manual rules:
	// Proxy list
	for _, d := range manual.Proxy.Domains {
		snapshot.Services[d] = "proxy"
	}
	for _, ip := range manual.Proxy.IPs {
		snapshot.Services[ip] = "proxy"
	}
	for _, p := range manual.Proxy.Processes {
		snapshot.Services[p] = "proxy"
	}
	for _, f := range manual.Proxy.Folders {
		snapshot.Services[f] = "proxy"
	}

	// Bypass list
	for _, d := range manual.Bypass.Domains {
		snapshot.Services[d] = "bypass"
	}
	for _, ip := range manual.Bypass.IPs {
		snapshot.Services[ip] = "bypass"
	}
	for _, p := range manual.Bypass.Processes {
		snapshot.Services[p] = "bypass"
	}
	for _, f := range manual.Bypass.Folders {
		snapshot.Services[f] = "bypass"
	}

	return snapshot
}

// ApplyRoutingSnapshotToState validates and transforms snapshot data into candidate RoutingState and Choices.
func ApplyRoutingSnapshotToState(
	snapshot *RoutingSnapshot,
	currentRouting routing.RoutingState,
	catalog Catalog,
) (routing.RoutingState, Choices, error) {
	nextRouting := routing.DefaultRoutingState()
	if snapshot.Mode != "" {
		nextRouting.Mode = routing.RoutingMode(snapshot.Mode)
	} else {
		nextRouting.Mode = currentRouting.Mode
	}
	nextRouting.Enabled = true

	// Apply services (manual rules)
	for rule, action := range snapshot.Services {
		if action == "block" {
			// A blocked rule is neither proxied nor bypassed
			continue
		}

		category := ""
		if snapshot.parsedCategories != nil {
			category = snapshot.parsedCategories[rule]
		}
		if category == "" {
			if _, err := netip.ParsePrefix(rule); err == nil {
				category = "ips"
			} else if _, err := netip.ParseAddr(rule); err == nil {
				category = "ips"
			} else if strings.Contains(rule, "/") && !strings.Contains(rule, "://") {
				category = "folders"
			} else if !strings.Contains(rule, ".") || hasExecExtension(rule) {
				category = "processes"
			} else {
				category = "domains"
			}
		}

		normalized, err := routing.NormalizeValue(category, rule)
		if err != nil {
			return routing.RoutingState{}, nil, fmt.Errorf("некорректное правило %q: %w", rule, err)
		}

		targetMode := routing.RoutingMode(action)
		if err := nextRouting.ChangeRule(targetMode, category, normalized, false); err != nil {
			return routing.RoutingState{}, nil, fmt.Errorf("не удалось применить правило %q: %w", rule, err)
		}
	}

	// Apply groups
	nextChoices := Choices{
		routing.ModeProxy:  make(map[string]bool),
		routing.ModeBypass: make(map[string]bool),
	}

	for id, action := range snapshot.Groups {
		if !validID.MatchString(id) {
			return routing.RoutingState{}, nil, fmt.Errorf("некорректный ID группы %q", id)
		}
		switch action {
		case "proxy":
			nextChoices[routing.ModeProxy][id] = true
			nextChoices[routing.ModeBypass][id] = false
		case "bypass":
			nextChoices[routing.ModeBypass][id] = true
			nextChoices[routing.ModeProxy][id] = false
		case "block":
			nextChoices[routing.ModeProxy][id] = false
			nextChoices[routing.ModeBypass][id] = false
		}
	}

	return nextRouting, nextChoices, nil
}
