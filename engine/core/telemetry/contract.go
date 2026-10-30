package telemetry

import (
	"fmt"
	"regexp"
	"sort"
)

// This file is docs/TELEMETRY.md §4–§5 in code: the full list of events,
// their fields and the error vocabulary. Anything not listed here is
// refused, so a new field cannot leave the device without a change to both.

type fieldKind int

const (
	kindEnum fieldKind = iota
	kindBool
	kindSeconds // non-negative whole seconds
	kindMinutes // rounded down to 10
	kindCount   // folded into 0 | 1 | 2-5 | 6+ (or the field's own buckets)
	kindVersion // semantic version
	kindErrCode // one of errorCodes, or empty
)

type fieldSpec struct {
	kind    fieldKind
	values  []string // kindEnum
	buckets []int    // kindCount: lower bounds after 0 and 1, e.g. {2, 6}
}

var (
	engines = []string{"singbox", "xray"}
	protos  = []string{"vless-reality", "vless-xhttp", "vmess", "trojan", "shadowsocks", "hysteria2", "other"}
)

var eventSpecs = map[string]map[string]fieldSpec{
	"install": {},
	"launch":  {"cold": {kind: kindBool}},
	"first_connect_ok": {
		"engine":             {kind: kindEnum, values: engines},
		"proto":              {kind: kindEnum, values: protos},
		"secs_since_install": {kind: kindSeconds},
	},
	"connect": {
		"engine": {kind: kindEnum, values: engines},
		"proto":  {kind: kindEnum, values: protos},
		"ok":     {kind: kindBool},
		"secs":   {kind: kindSeconds},
		"error":  {kind: kindErrCode},
	},
	"daily_active": {
		"connected_minutes": {kind: kindMinutes},
		"profiles":          {kind: kindCount, buckets: []int{2, 6}},
	},
	"engine_switch": {
		"from": {kind: kindEnum, values: engines},
		"to":   {kind: kindEnum, values: engines},
	},
	"profile_import": {
		"kind":  {kind: kindEnum, values: []string{"link", "subscription", "file"}},
		"ok":    {kind: kindBool},
		"count": {kind: kindCount, buckets: []int{2, 11}},
		"error": {kind: kindErrCode},
	},
	"subscription_sync": {
		"ok":      {kind: kindBool},
		"added":   {kind: kindCount, buckets: []int{2}},
		"removed": {kind: kindCount, buckets: []int{2}},
		"error":   {kind: kindErrCode},
	},
	"routing_apply": {
		"mode":   {kind: kindEnum, values: []string{"bypass", "proxy"}},
		"ok":     {kind: kindBool},
		"source": {kind: kindEnum, values: []string{"manual", "catalog", "import"}},
		"error":  {kind: kindErrCode},
	},
	"update": {
		"step":       {kind: kindEnum, values: []string{"found", "downloaded", "applied", "rolled_back", "failed"}},
		"to_version": {kind: kindVersion},
		"error":      {kind: kindErrCode},
	},
	"ui_open": {
		"screen": {kind: kindEnum, values: []string{"settings", "logs", "routing", "profiles", "support", "donate"}},
	},
	"cta": {
		"kind": {kind: kindEnum, values: []string{"subscribe", "renew", "donate"}},
	},
}

// errorCodes is the §5 vocabulary. A new code is a change to the contract.
var errorCodes = map[string]bool{
	"engine.start": true, "engine.crash": true, "engine.config": true,
	"tunnel.dns": true, "tunnel.route": true, "tunnel.down": true,
	"import.parse": true, "import.fetch": true,
	"sync.http": true, "sync.decode": true, "sync.apply": true,
	"routing.compile": true, "routing.apply": true,
	"update.check": true, "update.download": true, "update.verify": true, "update.apply": true,
	"service.ipc": true, "service.start": true,
	"ui.exception": true,
}

var components = map[string]bool{
	"engine": true, "singbox": true, "xray": true, "service": true, "updater": true, "ui": true,
}

var reVersion = regexp.MustCompile(`^\d{1,4}\.\d{1,4}\.\d{1,6}$`)

// normalize checks an event against its spec and returns the fields as they
// will be sent: counts folded into buckets, minutes rounded. Unknown events,
// unknown fields and out-of-vocabulary values are refused, not dropped
// silently, so a caller's mistake shows up in tests.
func normalize(name string, fields map[string]any) (map[string]any, error) {
	spec, ok := eventSpecs[name]
	if !ok {
		return nil, fmt.Errorf("telemetry: unknown event %q", name)
	}
	out := make(map[string]any, len(fields))
	for k, v := range fields {
		f, ok := spec[k]
		if !ok {
			return nil, fmt.Errorf("telemetry: event %q has no field %q", name, k)
		}
		nv, err := normalizeField(f, v)
		if err != nil {
			return nil, fmt.Errorf("telemetry: %s.%s: %w", name, k, err)
		}
		if nv != nil {
			out[k] = nv
		}
	}
	return out, nil
}

func normalizeField(f fieldSpec, v any) (any, error) {
	switch f.kind {
	case kindEnum:
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("want string, got %T", v)
		}
		for _, allowed := range f.values {
			if s == allowed {
				return s, nil
			}
		}
		return nil, fmt.Errorf("%q is not one of %v", s, f.values)
	case kindBool:
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("want bool, got %T", v)
		}
		return b, nil
	case kindSeconds, kindMinutes, kindCount:
		n, err := asInt(v)
		if err != nil {
			return nil, err
		}
		if n < 0 {
			return nil, fmt.Errorf("negative %d", n)
		}
		switch f.kind {
		case kindMinutes:
			return n / 10 * 10, nil
		case kindCount:
			return bucket(n, f.buckets), nil
		}
		return n, nil
	case kindVersion:
		s, ok := v.(string)
		if !ok || !reVersion.MatchString(s) {
			return nil, fmt.Errorf("%v is not a version", v)
		}
		return s, nil
	case kindErrCode:
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("want string, got %T", v)
		}
		if s == "" {
			return nil, nil
		}
		if !errorCodes[s] {
			return nil, fmt.Errorf("unknown error code %q", s)
		}
		return s, nil
	}
	return nil, fmt.Errorf("unhandled field kind %d", f.kind)
}

// bucket folds n into "0", "1", then ranges starting at each bound:
// bounds {2, 6} → "2-5", "6+".
func bucket(n int, bounds []int) string {
	if n <= 1 {
		return fmt.Sprint(n)
	}
	b := append([]int(nil), bounds...)
	sort.Ints(b)
	for i := len(b) - 1; i >= 0; i-- {
		if n < b[i] {
			continue
		}
		if i == len(b)-1 {
			return fmt.Sprintf("%d+", b[i])
		}
		if b[i+1]-1 == b[i] {
			return fmt.Sprint(b[i])
		}
		return fmt.Sprintf("%d-%d", b[i], b[i+1]-1)
	}
	return fmt.Sprint(n)
}

func asInt(v any) (int, error) {
	switch n := v.(type) {
	case int:
		return n, nil
	case int64:
		return int(n), nil
	case float64: // JSON from the UI
		if n != float64(int(n)) {
			return 0, fmt.Errorf("want whole number, got %v", n)
		}
		return int(n), nil
	}
	return 0, fmt.Errorf("want number, got %T", v)
}
