// Command gen builds engine/core/netinfo/data/{asn,area}.csv.gz from the
// free DB-IP Lite datasets (CC BY 4.0, https://db-ip.com/db/lite.php):
//
//	go run ./core/netinfo/gen \
//	    -asn  dbip-asn-lite-YYYY-MM.csv.gz \
//	    -city dbip-city-lite-YYYY-MM.csv.gz \
//	    -out  core/netinfo/data
//
// -asn also accepts iptoasn.com's ip2asn-v4.tsv[.gz]. Inputs may be plain
// or gzipped.
//
// Only ranges located (by core/geoip) in -countries are kept, which keeps
// the embedded data small. The area is coarsened here, on purpose: a range
// gets its city only if that city is in bigCities, otherwise its region.
// "Small town + small provider" can narrow a user down to a handful of
// people, so that granularity never makes it into the binary at all.
package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"NekkoVPN/core/geoip"
)

// bigCities maps every spelling DB-IP uses for a city of ~1M+ people to one
// canonical slug. Everything not listed is reported as its region.
var bigCities = map[string]string{
	"moscow":           "moscow",
	"saint-petersburg": "saint-petersburg",
	"st-petersburg":    "saint-petersburg",
	"sankt-peterburg":  "saint-petersburg",
	"novosibirsk":      "novosibirsk",
	"yekaterinburg":    "yekaterinburg",
	"ekaterinburg":     "yekaterinburg",
	"kazan":            "kazan",
	"nizhniy-novgorod": "nizhny-novgorod",
	"nizhny-novgorod":  "nizhny-novgorod",
	"chelyabinsk":      "chelyabinsk",
	"krasnoyarsk":      "krasnoyarsk",
	"samara":           "samara",
	"ufa":              "ufa",
	"rostov-on-don":    "rostov-on-don",
	"rostov-na-donu":   "rostov-on-don",
	"omsk":             "omsk",
	"krasnodar":        "krasnodar",
	"voronezh":         "voronezh",
	"perm":             "perm",
	"volgograd":        "volgograd",
}

type span struct {
	start, end uint32
	val        string
}

func main() {
	asnPath := flag.String("asn", "", "DB-IP ASN Lite CSV or iptoasn ip2asn-v4 TSV")
	cityPath := flag.String("city", "", "DB-IP City Lite CSV")
	countries := flag.String("countries", "ru", "comma-separated ISO2 codes to keep")
	out := flag.String("out", "", "output directory")
	flag.Parse()
	if *asnPath == "" || *cityPath == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: gen -asn <file> -city <file> [-countries ru] -out <dir>")
		os.Exit(2)
	}
	keep := map[string]bool{}
	for _, c := range strings.Split(*countries, ",") {
		if c = strings.ToLower(strings.TrimSpace(c)); c != "" {
			keep[c] = true
		}
	}

	asn, err := readASN(*asnPath, keep)
	if err != nil {
		fail("asn: %v", err)
	}
	area, err := readArea(*cityPath, keep)
	if err != nil {
		fail("city: %v", err)
	}
	if err := write(filepath.Join(*out, "asn.csv.gz"), asn); err != nil {
		fail("write asn: %v", err)
	}
	if err := write(filepath.Join(*out, "area.csv.gz"), area); err != nil {
		fail("write area: %v", err)
	}
	fmt.Printf("asn: %d ranges, area: %d ranges\n", len(asn), len(area))
}

func fail(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

// readASN accepts DB-IP ("start,end,asn,org") and iptoasn
// ("start\tend\tasn\tcountry\tdesc"); the separator is sniffed.
func readASN(path string, keep map[string]bool) ([]span, error) {
	var out []span
	err := eachRecord(path, func(rec []string) {
		if len(rec) < 3 {
			return
		}
		start, end, ok := v4Range(rec[0], rec[1])
		if !ok || !located(start, keep) {
			return
		}
		n, err := strconv.ParseUint(strings.TrimPrefix(strings.ToUpper(rec[2]), "AS"), 10, 32)
		if err != nil || n == 0 { // 0 = "not routed" in iptoasn
			return
		}
		out = append(out, span{start, end, strconv.FormatUint(n, 10)})
	})
	return merge(out), err
}

// readArea reads DB-IP City Lite:
// "start,end,continent,country,stateprov,city,lat,lon".
func readArea(path string, keep map[string]bool) ([]span, error) {
	var out []span
	err := eachRecord(path, func(rec []string) {
		if len(rec) < 6 || !keep[strings.ToLower(rec[3])] {
			return
		}
		start, end, ok := v4Range(rec[0], rec[1])
		if !ok {
			return
		}
		val := bigCities[slug(rec[5])]
		if val == "" {
			val = slug(rec[4])
		}
		if val == "" {
			return
		}
		out = append(out, span{start, end, val})
	})
	return merge(out), err
}

func located(start uint32, keep map[string]bool) bool {
	ip := net.IPv4(byte(start>>24), byte(start>>16), byte(start>>8), byte(start))
	code, ok := geoip.LookupIP(ip)
	return ok && keep[code]
}

func eachRecord(path string, fn func([]string)) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var r io.Reader = bytes.NewReader(raw)
	if len(raw) > 2 && raw[0] == 0x1f && raw[1] == 0x8b {
		zr, err := gzip.NewReader(r)
		if err != nil {
			return err
		}
		defer zr.Close()
		r = zr
	}
	br := bufio.NewReader(r)
	cr := csv.NewReader(br)
	if head, _ := br.Peek(512); bytes.IndexByte(head, '\t') >= 0 {
		cr.Comma = '\t'
	}
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	cr.ReuseRecord = true
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			continue
		}
		fn(rec)
	}
}

func v4Range(a, b string) (uint32, uint32, bool) {
	s, ok1 := v4(a)
	e, ok2 := v4(b)
	return s, e, ok1 && ok2 && e >= s
}

func v4(s string) (uint32, bool) {
	ip := net.ParseIP(strings.TrimSpace(s)).To4()
	if ip == nil {
		return 0, false
	}
	return uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3]), true
}

// slug lowercases and turns every run of non-alphanumerics into one "-".
func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

// merge sorts spans and joins adjacent ones carrying the same value — the
// coarsened area data collapses dramatically under this.
func merge(in []span) []span {
	sort.Slice(in, func(i, j int) bool { return in[i].start < in[j].start })
	var out []span
	for _, s := range in {
		if n := len(out); n > 0 && out[n-1].val == s.val && uint64(out[n-1].end)+1 >= uint64(s.start) {
			if s.end > out[n-1].end {
				out[n-1].end = s.end
			}
			continue
		}
		out = append(out, s)
	}
	return out
}

func write(path string, spans []span) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := gzip.NewWriter(f)
	w := csv.NewWriter(zw)
	for _, s := range spans {
		if err := w.Write([]string{
			strconv.FormatUint(uint64(s.start), 10),
			strconv.FormatUint(uint64(s.end), 10),
			s.val,
		}); err != nil {
			return err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return err
	}
	return zw.Close()
}
