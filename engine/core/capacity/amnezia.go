package capacity

import (
	"bytes"
	"compress/zlib"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"sort"
	"strings"
)

// amneziaKey identifies the actual AWG transport, not its compressed document
// or account peer. Kept byte-for-byte compatible by shared contract vectors.
func amneziaKey(link string) string {
	encoded := strings.SplitN(strings.SplitN(link[6:], "#", 2)[0], "?", 2)[0]
	if len(encoded) > 2<<20 {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(encoded, "="))
	if err != nil || len(raw) < 5 || len(raw) > 2<<20 {
		return ""
	}
	reader, err := zlib.NewReader(bytes.NewReader(raw[4:]))
	if err != nil {
		return ""
	}
	defer reader.Close()
	doc, err := io.ReadAll(io.LimitReader(reader, (1<<20)+1))
	if err != nil || len(doc) > 1<<20 || uint32(len(doc)) != binary.BigEndian.Uint32(raw[:4]) {
		return ""
	}
	var description struct {
		Containers []struct {
			Container string `json:"container"`
			AWG       struct {
				Config string `json:"last_config"`
			} `json:"awg"`
		} `json:"containers"`
	}
	if json.Unmarshal(doc, &description) != nil {
		return ""
	}
	for _, container := range description.Containers {
		if container.Container != "amnezia-awg" {
			continue
		}
		section := ""
		fields := []string{}
		endpoint, public := false, false
		peers := 0
		seen := map[string]bool{}
		for _, line := range strings.Split(container.AWG.Config, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
				continue
			}
			if strings.HasPrefix(line, "[") {
				section = strings.ToLower(line)
				if section == "[peer]" {
					peers++
				}
				continue
			}
			key, value, ok := strings.Cut(line, "=")
			if !ok {
				return ""
			}
			key, value = strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(value)
			if section == "[interface]" && (key == "privatekey" || key == "address" || key == "dns") {
				continue
			}
			if section != "[interface]" && section != "[peer]" {
				return ""
			}
			name := section + key
			if seen[name] {
				return ""
			}
			seen[name] = true
			if section == "[peer]" && key == "endpoint" {
				endpoint = value != ""
				value = strings.ToLower(value)
			}
			if section == "[peer]" && key == "publickey" {
				public = value != ""
			}
			fields = append(fields, name+"="+value)
		}
		if !endpoint || !public || peers != 1 {
			return ""
		}
		sort.Strings(fields)
		sum := sha256.Sum256([]byte("hikkari-awg-capacity-v1\n" + strings.Join(fields, "\n")))
		return hex.EncodeToString(sum[:])
	}
	return ""
}
