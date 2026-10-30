package xray

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// URI masks mirror the signed fleet record, never arbitrary Xray settings.
type packetMask struct {
	Type     string `json:"type"`
	Password string `json:"password,omitempty"`
	Junk     []struct {
		Len   []int `json:"len"`
		Delay []int `json:"delay_ms"`
	} `json:"junk,omitempty"`
	Reset []int `json:"reset_secs,omitempty"`
}

func certificatePin(pin string) (string, error) {
	pin = strings.ToLower(strings.ReplaceAll(pin, ":", ""))
	raw, err := hex.DecodeString(pin)
	if err != nil || len(raw) != 32 {
		return "", fmt.Errorf("Xray: pinSHA256 должен содержать SHA-256 сертификата (64 hex-символа)")
	}
	return pin, nil
}
func packetMasks(encoded string) ([]interface{}, error) {
	if len(encoded) > 16384 {
		return nil, fmt.Errorf("Xray: слишком большой набор масок Hysteria2")
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("Xray: finalmask должен быть base64url JSON")
	}
	var masks []packetMask
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&masks); err != nil {
		return nil, fmt.Errorf("Xray: неверный JSON масок Hysteria2")
	}
	if err := dec.Decode(new(interface{})); err != io.EOF {
		return nil, fmt.Errorf("Xray: лишние данные после масок")
	}
	if len(masks) < 1 || len(masks) > 8 {
		return nil, fmt.Errorf("Xray: требуется от 1 до 8 масок")
	}
	span := func(v []int, max int) (string, error) {
		if len(v) != 2 || v[0] < 0 || v[1] < v[0] || v[1] > max {
			return "", fmt.Errorf("Xray: неверный диапазон маски")
		}
		return strconv.Itoa(v[0]) + "-" + strconv.Itoa(v[1]), nil
	}
	out := make([]interface{}, 0, len(masks))
	for _, m := range masks {
		switch m.Type {
		case "salamander":
			if len(m.Password) < 16 || len(m.Password) > 256 || m.Junk != nil || m.Reset != nil {
				return nil, fmt.Errorf("Xray: неверная маска Salamander")
			}
			out = append(out, object{"type": "salamander", "settings": object{"password": m.Password}})
		case "noise":
			if m.Password != "" || len(m.Junk) < 1 || len(m.Junk) > 32 {
				return nil, fmt.Errorf("Xray: неверная маска Noise")
			}
			reset, err := span(m.Reset, 86400)
			if err != nil {
				return nil, err
			}
			junk := make([]interface{}, 0, len(m.Junk))
			for _, j := range m.Junk {
				size, err := span(j.Len, 65535)
				if err != nil {
					return nil, err
				}
				delay, err := span(j.Delay, 60000)
				if err != nil {
					return nil, err
				}
				junk = append(junk, object{"rand": size, "delay": delay})
			}
			out = append(out, object{"type": "noise", "settings": object{"reset": reset, "noise": junk}})
		default:
			return nil, fmt.Errorf("Xray: неизвестная маска Hysteria2 %q", m.Type)
		}
	}
	return out, nil
}
