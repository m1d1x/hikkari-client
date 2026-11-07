package capacity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"net/url"
	"strings"
	"time"
)

const Header = "Hikkari-Relay-Capacity"

// Key identifies the transport configuration without the subscriber credential
// or presentation label. Query ordering is canonical on both ends of the contract.
func Key(link string) string {
	u, err := url.Parse(link)
	if err != nil || u.Host == "" {
		return ""
	}
	u.User, u.Fragment, u.RawFragment = nil, "", ""
	u.Scheme, u.Host = strings.ToLower(u.Scheme), strings.ToLower(u.Host)
	q := u.Query()
	q.Del("hikkari-node")
	u.RawQuery = q.Encode()
	sum := sha256.Sum256([]byte(u.String()))
	return hex.EncodeToString(sum[:])
}

type Metric struct {
	Key        string  `json:"key"`
	MeasuredAt int64   `json:"measuredAt"`
	ExpiresAt  int64   `json:"expiresAt"`
	Mbps       float64 `json:"mbps"`
	Limited    bool    `json:"limited"`
	Points     int     `json:"points"`
}

func (m Metric) Fresh(now time.Time) bool {
	key, err := hex.DecodeString(m.Key)
	return err == nil && len(key) == 32 && m.Key == strings.ToLower(m.Key) && m.Points > 0 && m.Points <= 128 &&
		!math.IsNaN(m.Mbps) && !math.IsInf(m.Mbps, 0) && m.Mbps > 0 && m.Mbps <= 1000000 &&
		m.MeasuredAt <= now.Add(2*time.Minute).Unix() && m.ExpiresAt > now.Unix() && m.ExpiresAt > m.MeasuredAt && m.ExpiresAt-m.MeasuredAt <= 5400
}
func Decode(raw string) []Metric {
	var values []Metric
	if len(raw) > 8192 || json.Unmarshal([]byte(raw), &values) != nil || len(values) > 24 {
		return nil
	}
	seen := map[string]bool{}
	out := []Metric{}
	for _, m := range values {
		if m.Fresh(time.Now()) && !seen[m.Key] {
			out = append(out, m)
			seen[m.Key] = true
		}
	}
	return out
}
