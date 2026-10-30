// Package downdetector turns the uptime samples the engine already collects
// into periodic, anonymous reports for the shared downdetector (see
// docs/DOWNDETECTOR.md). It adds no probes of its own.
//
// Phase 0: reports are only queued to a local file. Nothing is sent yet.
package downdetector

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"NekkoVPN/core/uptime"
)

// Window is the reporting period. Report windows are aligned to multiples
// of it, so no timestamp finer than that ever leaves the device.
const Window = 5 * time.Minute

// Report is everything one report carries — the full list. It is a public
// promise (docs/DOWNDETECTOR.md §4.1): a new field here means updating that
// section first.
type Report struct {
	Install string       `json:"install"` // random, rotated weekly
	ASN     uint32       `json:"asn"`     // 0 = unknown
	Country string       `json:"country"` // ISO2, "" = unknown
	Region  string       `json:"region"`  // big city or region, "" = unknown
	Window  [2]int64     `json:"window"`  // [start, end) unix seconds
	Servers []ServerStat `json:"servers"`
}

// ServerStat aggregates one server's samples of one Source in the window.
type ServerStat struct {
	ID     string        `json:"id"`
	Proto  string        `json:"proto"`
	Src    uptime.Source `json:"src"`
	OK     int           `json:"ok"`
	Fail   int           `json:"fail"`
	RttP50 int           `json:"rtt_p50"` // median RTT of successful samples
}

// Server maps a local profile to its reportable identity. Profile is the
// uptime history key and never leaves the device.
type Server struct {
	Profile string
	ID      string
	Proto   string
	// Private servers are reported only when the user opted in
	// (Settings.SharePrivate).
	Private bool
}

// Net is the user's network as resolved locally (see core/netinfo).
type Net struct {
	ASN     uint32
	Country string
	Region  string
}

// ServerID is the reportable id of a server the user found elsewhere
// (subscription or a hand-added link). Hashing keeps the address out of
// the report body, but it is not anonymisation: the IPv4 space is small
// enough to brute-force, which is why private servers need an opt-in.
func ServerID(host string, port int, proto string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%s", strings.ToLower(host), port, proto)))
	return "s:" + hex.EncodeToString(sum[:16])
}

// Build is the only place a report is assembled. servers must already be
// filtered to what the user allows to share.
func Build(install string, n Net, start time.Time, servers []Server, history map[string][]uptime.Sample) Report {
	from, to := start.Unix(), start.Add(Window).Unix()
	r := Report{
		Install: install,
		ASN:     n.ASN,
		Country: n.Country,
		Region:  n.Region,
		Window:  [2]int64{from, to},
		Servers: []ServerStat{},
	}
	for _, s := range servers {
		bySrc := map[uptime.Source]*ServerStat{}
		rtts := map[uptime.Source][]int{}
		for _, smp := range history[s.Profile] {
			if smp.Ts < from || smp.Ts >= to || smp.Src == "" {
				continue
			}
			st := bySrc[smp.Src]
			if st == nil {
				st = &ServerStat{ID: s.ID, Proto: s.Proto, Src: smp.Src}
				bySrc[smp.Src] = st
			}
			if smp.OK {
				st.OK++
				rtts[smp.Src] = append(rtts[smp.Src], smp.RttMs)
			} else {
				st.Fail++
			}
		}
		for src, st := range bySrc {
			st.RttP50 = median(rtts[src])
			r.Servers = append(r.Servers, *st)
		}
	}
	sort.Slice(r.Servers, func(i, j int) bool {
		if r.Servers[i].ID != r.Servers[j].ID {
			return r.Servers[i].ID < r.Servers[j].ID
		}
		return r.Servers[i].Src < r.Servers[j].Src
	})
	return r
}

func median(v []int) int {
	if len(v) == 0 {
		return 0
	}
	sort.Ints(v)
	return v[len(v)/2]
}
