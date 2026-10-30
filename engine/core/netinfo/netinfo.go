// Package netinfo resolves the user's own IP to the network facts the
// downdetector reports: the provider (ASN) and a coarse area (a big city,
// or else the region). Everything is looked up in databases embedded into
// the binary — the IP is never sent anywhere to be resolved.
//
// The area is coarsened when the database is built (see ./gen), not at
// lookup time: small towns are folded into their region before the data
// ever ships, so the binary cannot resolve anything finer than that.
//
// Both databases are gzip-compressed CSV "start,end,value" (IPv4 endpoints
// inclusive, as uint32), sorted by start. They ship empty until generated;
// lookups then simply report nothing.
package netinfo

import (
	"bufio"
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/csv"
	"net"
	"sort"
	"strconv"
	"sync"
)

//go:embed data/asn.csv.gz
var asnGz []byte

//go:embed data/area.csv.gz
var areaGz []byte

// Info is what we know about an IP. Zero values mean "unknown".
type Info struct {
	ASN  uint32 `json:"asn"`
	Area string `json:"area"`
}

type span struct {
	start uint32
	end   uint32
	val   string
}

type table []span

var (
	dbOnce  sync.Once
	asnTab  table
	areaTab table
)

func loadDB() {
	dbOnce.Do(func() {
		asnTab = parse(asnGz)
		areaTab = parse(areaGz)
	})
}

// parse decodes one embedded database. Malformed rows are skipped rather
// than failing the whole table.
func parse(gz []byte) table {
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil
	}
	defer zr.Close()
	r := csv.NewReader(bufio.NewReader(zr))
	r.FieldsPerRecord = 3
	var t table
	for {
		rec, err := r.Read()
		if err != nil {
			if _, ok := err.(*csv.ParseError); ok {
				continue
			}
			break
		}
		start, err1 := strconv.ParseUint(rec[0], 10, 32)
		end, err2 := strconv.ParseUint(rec[1], 10, 32)
		if err1 != nil || err2 != nil || end < start || rec[2] == "" {
			continue
		}
		t = append(t, span{start: uint32(start), end: uint32(end), val: rec[2]})
	}
	// gen already sorts by start, but don't trust the embedded asset blindly.
	sort.Slice(t, func(i, j int) bool { return t[i].start < t[j].start })
	return t
}

func (t table) lookup(v uint32) (string, bool) {
	i := sort.Search(len(t), func(i int) bool { return t[i].start > v })
	if i == 0 {
		return "", false
	}
	s := t[i-1]
	if v > s.end {
		return "", false
	}
	return s.val, true
}

func ipToUint32(ip net.IP) (uint32, bool) {
	v4 := ip.To4()
	if v4 == nil {
		return 0, false
	}
	return uint32(v4[0])<<24 | uint32(v4[1])<<16 | uint32(v4[2])<<8 | uint32(v4[3]), true
}

// Lookup resolves ip against the embedded databases. IPv6 and addresses
// outside every known range yield a zero Info.
func Lookup(ip net.IP) Info {
	loadDB()
	return lookupIn(asnTab, areaTab, ip)
}

func lookupIn(asn, area table, ip net.IP) Info {
	v, ok := ipToUint32(ip)
	if !ok {
		return Info{}
	}
	var info Info
	if s, ok := asn.lookup(v); ok {
		if n, err := strconv.ParseUint(s, 10, 32); err == nil {
			info.ASN = uint32(n)
		}
	}
	info.Area, _ = area.lookup(v)
	return info
}

// Available reports whether the ASN database was generated into this
// build. Without it every report goes out with an unknown provider.
func Available() bool {
	loadDB()
	return len(asnTab) > 0
}
