package capacity

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDecodeAcceptsMissingSpeedButRejectsInventedHistory(t *testing.T) {
	now := time.Now().Unix()
	m := Metric{Key: strings.Repeat("a", 64), MeasuredAt: now, ExpiresAt: now + 5400, Points: 2, RTTMS: 100, History: []Point{{Ts: now / 1800 * 1800, RTTMS: 100}}}
	raw, _ := json.Marshal([]Metric{m})
	if len(Decode(string(raw))) != 1 {
		t.Fatal("latency-only observation discarded")
	}
	m.History = append(m.History, m.History[0])
	raw, _ = json.Marshal([]Metric{m})
	if len(Decode(string(raw))) != 0 {
		t.Fatal("duplicate time accepted")
	}
	m.History = nil
	m.ExpiresAt = now
	raw, _ = json.Marshal([]Metric{m})
	if len(Decode(string(raw))) != 0 {
		t.Fatal("stale result accepted")
	}
}
