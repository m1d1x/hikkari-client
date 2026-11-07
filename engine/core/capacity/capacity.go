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
	if strings.HasPrefix(strings.ToLower(link), "vpn://") {
		return amneziaKey(link)
	}
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

// Point is one half-hour aggregate from owned RU observation points.
type Point struct {
	Ts    int64   `json:"ts"`
	RTTMS int     `json:"rttMs,omitempty"`
	Mbps  float64 `json:"mbps,omitempty"`
}

type Metric struct {
	Key        string  `json:"key"`
	MeasuredAt int64   `json:"measuredAt"`
	ExpiresAt  int64   `json:"expiresAt"`
	Mbps       float64 `json:"mbps"`
	Limited    bool    `json:"limited"`
	Points     int     `json:"points"`
	RTTMS      int     `json:"rttMs,omitempty"`
	History    []Point `json:"history,omitempty"`
}

func (m Metric) Fresh(now time.Time) bool {
	key, err := hex.DecodeString(m.Key)
	return err == nil && len(key) == 32 && m.Key == strings.ToLower(m.Key) && m.Points > 0 && m.Points <= 128 &&
		!math.IsNaN(m.Mbps) && !math.IsInf(m.Mbps, 0) && m.Mbps >= 0 && m.Mbps <= 1000000 && m.RTTMS >= 0 && m.RTTMS <= 30000 &&
		m.MeasuredAt <= now.Add(2*time.Minute).Unix() && m.ExpiresAt > now.Unix() && m.ExpiresAt > m.MeasuredAt && m.ExpiresAt-m.MeasuredAt <= 5400
}
func Decode(raw string) []Metric {
	var values []Metric
	if len(raw) > 512*1024 || json.Unmarshal([]byte(raw), &values) != nil || len(values) > 24 {
		return nil
	}
	seen := map[string]bool{}
	out := []Metric{}
	for _, m := range values {
		if m.Fresh(time.Now()) && !seen[m.Key] && validHistory(m) {
			out = append(out, m)
			seen[m.Key] = true
		}
	}
	return out
}

func validHistory(m Metric) bool {
	if len(m.History) > 48 {
		return false
	}
	var previous int64
	for _, p := range m.History {
		if p.Ts <= previous || p.Ts > time.Now().Add(2*time.Minute).Unix() || p.Ts < time.Now().Add(-25*time.Hour).Unix() || p.RTTMS < 0 || p.RTTMS > 30000 || math.IsNaN(p.Mbps) || math.IsInf(p.Mbps, 0) || p.Mbps < 0 || p.Mbps > 1000000 {
			return false
		}
		previous = p.Ts
	}
	return true
}
