package legacy

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// awg3Kind is how an AmneziaWG 3.x [Interface] parameter is written.
type awg3Kind int

const (
	// awg3Key is the base64 32-byte header protection key (`awg genkey`).
	awg3Key awg3Kind = iota
	// awg3Range is a number or an inclusive "min-max" range.
	awg3Range
	// awg3Switch is a 0/1 switch; only "on" differs from the default.
	awg3Switch
)

// AWG3Field is one AmneziaWG 3.x parameter: its name in a .conf file and in
// an Amnezia container (Ini), and its name in the stored profile and the
// sing-box-lx endpoint (Key).
type AWG3Field struct {
	Ini  string
	Key  string
	kind awg3Kind
}

// AWG3Fields are the parameters AmneziaWG 3.x adds on top of 2.0, in the
// order the .conf file lists them. The profile keeps every one under the
// sing-box-lx option name, so the core receives them as imported; dropping
// one turns the profile into a 2.0 one the server does not answer.
var AWG3Fields = []AWG3Field{
	{"HeaderProtectionKey", "header_protection_key", awg3Key},
	{"ContentPaddingAddition", "content_padding_addition", awg3Range},
	{"RekeyAfterTime", "rekey_after_time", awg3Range},
	{"RekeyTimeout", "rekey_timeout", awg3Range},
	{"RejectAfterTime", "reject_after_time", awg3Range},
	{"KeepaliveTimeout", "keepalive_timeout", awg3Range},
	{"MaxHandshakeAttempts", "max_handshake_attempts", awg3Range},
	{"RandomTrailers", "random_trailers", awg3Switch},
	{"DisableCookies", "disable_cookies", awg3Switch},
}

// ParseAWG3 turns a field's text into the profile value. ok is false when
// the text means "not set" (empty, a zero range, a switch that is off). A
// value the core would not take is refused with the field's name: dropping it
// would silently downgrade the profile. The header protection key is never
// echoed back.
func (f AWG3Field) ParseAWG3(raw string) (value interface{}, ok bool, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, false, nil
	}
	switch f.kind {
	case awg3Key:
		key, err := base64.StdEncoding.DecodeString(raw)
		if err != nil || len(key) != 32 {
			return nil, false, fmt.Errorf("amnezia: %s должен быть 32-байтным ключом в base64", f.Ini)
		}
		return raw, true, nil
	case awg3Range:
		spec, err := NormalizeAWGRange(raw)
		if err != nil {
			return nil, false, fmt.Errorf("amnezia: %s: %v", f.Ini, err)
		}
		return spec, spec != "", nil
	default:
		on, err := strconv.ParseBool(strings.ToLower(raw))
		if err != nil {
			return nil, false, fmt.Errorf("amnezia: %s должен быть 0 или 1, а не %q", f.Ini, raw)
		}
		return true, on, nil
	}
}

// exportAWG3 is the .conf value of the field in a stored profile, "" when
// the profile leaves it unset.
func (f AWG3Field) exportAWG3(c map[string]interface{}) string {
	switch f.kind {
	case awg3Switch:
		if on, _ := c[f.Key].(bool); on {
			return "1"
		}
		return ""
	default:
		return rangeText(c[f.Key])
	}
}

// NormalizeAWGRange mirrors the core's AWGRange: "N" or "N-M" of uint32 with
// N ≤ M, written back as "N" (also for "N-N") or "N-M", and "" for zero.
func NormalizeAWGRange(spec string) (string, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", nil
	}
	parts := strings.Split(spec, "-")
	if len(parts) > 2 {
		return "", fmt.Errorf("ожидается число или диапазон N-M, а не %q", spec)
	}
	start, err := strconv.ParseUint(strings.TrimSpace(parts[0]), 10, 32)
	if err != nil {
		return "", fmt.Errorf("ожидается число или диапазон N-M, а не %q", spec)
	}
	end := start
	if len(parts) == 2 {
		if end, err = strconv.ParseUint(strings.TrimSpace(parts[1]), 10, 32); err != nil {
			return "", fmt.Errorf("ожидается число или диапазон N-M, а не %q", spec)
		}
	}
	switch {
	case end < start:
		return "", fmt.Errorf("начало диапазона %q больше конца", spec)
	case end == 0:
		return "", nil
	case start == end:
		return strconv.FormatUint(start, 10), nil
	}
	return strconv.FormatUint(start, 10) + "-" + strconv.FormatUint(end, 10), nil
}

// rangeText reads a stored number-or-range value: the parser stores a string,
// a profile written as JSON elsewhere may carry a plain number.
func rangeText(v interface{}) string {
	switch n := v.(type) {
	case string:
		return strings.TrimSpace(n)
	case float64:
		if n > 0 {
			return strconv.FormatInt(int64(n), 10)
		}
	case int:
		if n > 0 {
			return strconv.Itoa(n)
		}
	case int64:
		if n > 0 {
			return strconv.FormatInt(n, 10)
		}
	}
	return ""
}
