// Command gen builds engine/core/geoip/data/ranges.csv.gz from a directory of
// ipdeny.com-style per-country zone files (one CIDR per line, filename
// "<iso2>.zone"). Run once whenever the dataset needs refreshing:
//
//	go run ./core/geoip/gen -src <zones-dir> -out core/geoip/data/ranges.csv.gz
//
// Output is a gzip-compressed CSV "start,end,code" (both endpoints inclusive,
// as uint32 IPv4 addresses), sorted by start, ready for go:embed + binary
// search in geoip.go.
package main

import (
	"bufio"
	"compress/gzip"
	"encoding/csv"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type ipRange struct {
	start uint32
	end   uint32
	code  string
}

func ipToUint32(ip net.IP) (uint32, bool) {
	v4 := ip.To4()
	if v4 == nil {
		return 0, false
	}
	return uint32(v4[0])<<24 | uint32(v4[1])<<16 | uint32(v4[2])<<8 | uint32(v4[3]), true
}

func main() {
	src := flag.String("src", "", "directory of <iso2>.zone CIDR files")
	out := flag.String("out", "", "output path for ranges.csv.gz")
	flag.Parse()
	if *src == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: gen -src <zones-dir> -out <ranges.csv.gz>")
		os.Exit(2)
	}

	entries, err := os.ReadDir(*src)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read src: %v\n", err)
		os.Exit(1)
	}

	var ranges []ipRange
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".zone") {
			continue
		}
		code := strings.ToLower(strings.TrimSuffix(name, ".zone"))
		f, err := os.Open(filepath.Join(*src, name))
		if err != nil {
			fmt.Fprintf(os.Stderr, "open %s: %v\n", name, err)
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			_, ipnet, err := net.ParseCIDR(line)
			if err != nil {
				continue
			}
			startIP, ok := ipToUint32(ipnet.IP)
			if !ok {
				continue // IPv6 zone — out of scope for v1
			}
			mask, _ := ipnet.Mask.Size()
			hostBits := 32 - mask
			var span uint32
			if hostBits >= 32 {
				span = 0xFFFFFFFF
			} else {
				span = (uint32(1) << uint(hostBits)) - 1
			}
			ranges = append(ranges, ipRange{start: startIP, end: startIP + span, code: code})
		}
		f.Close()
	}

	sort.Slice(ranges, func(i, j int) bool { return ranges[i].start < ranges[j].start })

	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir out dir: %v\n", err)
		os.Exit(1)
	}
	outF, err := os.Create(*out)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create out: %v\n", err)
		os.Exit(1)
	}
	defer outF.Close()
	gz := gzip.NewWriter(outF)
	w := csv.NewWriter(gz)
	for _, r := range ranges {
		_ = w.Write([]string{
			strconv.FormatUint(uint64(r.start), 10),
			strconv.FormatUint(uint64(r.end), 10),
			r.code,
		})
	}
	w.Flush()
	if err := w.Error(); err != nil {
		fmt.Fprintf(os.Stderr, "write csv: %v\n", err)
		os.Exit(1)
	}
	if err := gz.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "close gzip: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %d ranges to %s\n", len(ranges), *out)
}
