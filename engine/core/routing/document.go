package routing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
)

const SchemaVersion = 1
const MaxDocumentBytes = 4 << 20
const MaxRulesPerCategory = 2000

// Document is engine-independent user intent. It never contains native engine
// tags, server credentials, runtime status or machine-specific process paths.
// Sources remain separate so disabling a service cannot erase a manual rule.
type Document struct {
	SchemaVersion int         `json:"schemaVersion"`
	Enabled       bool        `json:"enabled"`
	Mode          RoutingMode `json:"mode"`
	Sources       []Source    `json:"sources"`
}

type SourceKind string

const (
	SourceManual   SourceKind = "manual"
	SourceCatalog  SourceKind = "catalog"
	SourceSnapshot SourceKind = "snapshot"
)

type Source struct {
	ID     string     `json:"id"`
	Kind   SourceKind `json:"kind"`
	Name   string     `json:"name"`
	Bypass Rules      `json:"bypass"`
	Proxy  Rules      `json:"proxy"`
}

// Rules retain foreign-platform process names across sharing. Resolving for
// an OS selects only its process list; it never rewrites the source document.
type Rules struct {
	Domains   []string            `json:"domains"`
	IPs       []string            `json:"ips"`
	Processes map[string][]string `json:"processes"`
}

var sourceID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,95}$`)
var domainLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func ValidPlatform(platform string) bool {
	return platform == "darwin" || platform == "windows" || platform == "linux"
}

// ValidateValues is the strict external boundary shared by documents and the
// service catalog. Interactive input uses NormalizeValue before this boundary.
// Never silently drop or broaden a rule in an imported document.
func ValidateValues(category string, entries []string) ([]string, error) {
	if len(entries) > MaxRulesPerCategory {
		return nil, fmt.Errorf("слишком много правил")
	}
	out := make([]string, 0, len(entries))
	seen := map[string]bool{}
	for _, value := range entries {
		if len(value) > 512 || strings.ContainsAny(value, "\x00\r\n") {
			return nil, fmt.Errorf("некорректное правило")
		}
		normal, err := NormalizeValue(category, value)
		if err != nil {
			return nil, err
		}
		if value != normal {
			return nil, fmt.Errorf("правило %q должно быть записано как %q", value, normal)
		}

		// Preserve process spelling: process matching may be case sensitive.
		if !seen[normal] {
			out = append(out, normal)
			seen[normal] = true
		}
	}
	return out, nil
}

func validateRules(r Rules) error {
	if _, err := ValidateValues("domains", r.Domains); err != nil {
		return err
	}
	if _, err := ValidateValues("ips", r.IPs); err != nil {
		return err
	}
	for platform, values := range r.Processes {
		if !ValidPlatform(platform) {
			return fmt.Errorf("неизвестная платформа %q", platform)
		}
		if _, err := ValidateValues("processes", values); err != nil {
			return err
		}
	}
	return nil
}

func (d Document) Validate() error {
	if d.SchemaVersion != SchemaVersion {
		return fmt.Errorf("неподдерживаемая версия маршрутизации: %d", d.SchemaVersion)
	}
	if d.Mode != ModeBypass && d.Mode != ModeProxy {
		return fmt.Errorf("неизвестный режим %q", d.Mode)
	}
	if d.Sources == nil || len(d.Sources) > 201 {
		return fmt.Errorf("некорректное число источников маршрутизации")
	}
	seen := map[string]bool{}
	for _, s := range d.Sources {
		if !sourceID.MatchString(s.ID) || seen[s.ID] {
			return fmt.Errorf("некорректный или повторный ID источника %q", s.ID)
		}
		seen[s.ID] = true
		if s.Kind != SourceManual && s.Kind != SourceCatalog && s.Kind != SourceSnapshot {
			return fmt.Errorf("неизвестный вид источника %q", s.Kind)
		}
		if strings.TrimSpace(s.Name) == "" || len(s.Name) > 100 {
			return fmt.Errorf("некорректное название источника %q", s.ID)
		}
		if err := validateRules(s.Bypass); err != nil {
			return fmt.Errorf("%s.bypass: %w", s.ID, err)
		}
		if err := validateRules(s.Proxy); err != nil {
			return fmt.Errorf("%s.proxy: %w", s.ID, err)
		}
	}
	return nil
}

// rejectDuplicateKeys prevents different JSON consumers from disagreeing about
// which occurrence of a field is authoritative, including nested sources.
func rejectDuplicateKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var value func() error
	value = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, compound := token.(json.Delim)
		if !compound {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return err
				}
				name := key.(string)
				if seen[name] {
					return fmt.Errorf("повторное поле %q", name)
				}
				seen[name] = true
				if err := value(); err != nil {
					return err
				}
			}
		case '[':
			for decoder.More() {
				if err := value(); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("некорректный JSON")
		}
		_, err = decoder.Token()
		return err
	}
	return value()
}

// exactObject also rejects case aliases (encoding/json otherwise accepts
// "Enabled" for "enabled"), keeping the wire shape identical in Go and Dart.
func exactObject(data []byte, required, optional []string) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, fmt.Errorf("ожидается JSON-объект")
	}
	known := map[string]bool{}
	for _, field := range append(append([]string{}, required...), optional...) {
		known[field] = true
	}
	for field := range object {
		if !known[field] {
			return nil, fmt.Errorf("неизвестное поле %q", field)
		}
	}
	for _, field := range required {
		raw, ok := object[field]
		if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil, fmt.Errorf("отсутствует поле %s", field)
		}
	}
	return object, nil
}

func validateDocumentShape(data []byte) error {
	envelope, err := exactObject(data, []string{"schemaVersion", "enabled", "mode", "sources"}, nil)
	if err != nil {
		return err
	}
	var sources []json.RawMessage
	if err := json.Unmarshal(envelope["sources"], &sources); err != nil {
		return err
	}
	for _, raw := range sources {
		source, err := exactObject(raw, []string{"id", "kind", "name", "bypass", "proxy"}, nil)
		if err != nil {
			return err
		}
		for _, mode := range []string{"bypass", "proxy"} {
			if _, err := exactObject(source[mode], nil, []string{"domains", "ips", "processes"}); err != nil {
				return err
			}
		}
	}
	return nil
}

// ParseDocument rejects unknown fields, future schemas and trailing data.
// Required envelope fields distinguish false/empty intent from missing input.
func ParseDocument(data []byte) (Document, error) {
	var d Document
	if len(data) > MaxDocumentBytes {
		return d, fmt.Errorf("документ маршрутизации превышает 4 МиБ")
	}
	if err := rejectDuplicateKeys(data); err != nil {
		return d, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&d); err != nil {
		return d, fmt.Errorf("маршрутизация: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return d, fmt.Errorf("лишние данные после маршрутизации")
	}
	if err := validateDocumentShape(data); err != nil {
		return d, err
	}

	return d, d.Validate()
}

// Resolve is deterministic and pure: OS selection is explicit, never global.
func (d Document) Resolve(platform string) (RoutingState, error) {
	if !ValidPlatform(platform) {
		return RoutingState{}, fmt.Errorf("неизвестная платформа %q", platform)
	}
	if err := d.Validate(); err != nil {
		return RoutingState{}, err
	}
	out := DefaultRoutingState()
	out.Enabled, out.Mode = d.Enabled, d.Mode
	for _, source := range d.Sources {
		mergeRules(&out.Bypass, source.Bypass, platform)
		mergeRules(&out.Proxy, source.Proxy, platform)
	}
	return out, nil
}

func mergeRules(dst *RouteList, src Rules, platform string) {
	dst.Domains = union(dst.Domains, src.Domains)
	dst.IPs = union(dst.IPs, src.IPs)
	dst.Processes = union(dst.Processes, src.Processes[platform])
}

func union(base, more []string) []string {
	out := append([]string{}, base...)
	seen := make(map[string]bool, len(base)+len(more))
	for _, value := range base {
		seen[value] = true
	}
	for _, value := range more {
		if !seen[value] {
			out = append(out, value)
			seen[value] = true
		}
	}
	return out
}

// ManualDocument adapts existing local policy without changing its disk format.
// Legacy unscoped processes belong to the OS of the installation reading them.
func ManualDocument(state RoutingState, platform string) Document {
	rules := func(list RouteList) Rules {
		return Rules{Domains: append([]string{}, list.Domains...), IPs: append([]string{}, list.IPs...), Processes: map[string][]string{platform: append([]string{}, list.Processes...)}}
	}
	return Document{SchemaVersion: SchemaVersion, Enabled: state.Enabled, Mode: state.Mode,
		Sources: []Source{{ID: "manual", Kind: SourceManual, Name: "Свои правила", Bypass: rules(state.Bypass), Proxy: rules(state.Proxy)}}}
}
