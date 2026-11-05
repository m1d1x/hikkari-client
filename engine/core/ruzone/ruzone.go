// Package ruzone is the Russian internet as a Hikkari exit refuses it. The
// edge servers (hikkari-control/edge/egress.go) drop every connection to a
// Russian domain zone, a key Russian ecosystem or a Russian IP allocation, so
// a VPN exit is never seen reaching Russian resources. Traffic the client
// sends there through the tunnel cannot arrive: the site simply does not open.
// The client therefore sends exactly this set direct.
//
// The data is the edge's own: cidrs.txt.gz is a byte copy of the edge's
// ru_cidrs.txt.gz and domains.txt lists its blockedDomains without the Xray
// "domain:" prefix. The edge tests compare the two when both repositories are
// checked out side by side.
package ruzone

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"io"
	"strings"
	"sync"
)

//go:embed domains.txt
var domainsTxt string

//go:embed cidrs.txt.gz
var cidrsGz []byte

var (
	domains = strings.Fields(domainsTxt)
	cidrs   = sync.OnceValue(loadCIDRs)
)

// Domains returns the domain suffixes: each matches the name itself and every
// subdomain. The slice is shared; callers must not modify it.
func Domains() []string { return domains }

// CIDRs returns the Russian IP allocations, IPv4 then IPv6. The slice is
// shared; callers must not modify it.
func CIDRs() []string { return cidrs() }

func loadCIDRs() []string {
	gz, err := gzip.NewReader(bytes.NewReader(cidrsGz))
	if err != nil {
		panic("ruzone: corrupt embedded cidrs.txt.gz: " + err.Error())
	}
	defer gz.Close()
	raw, err := io.ReadAll(gz)
	if err != nil {
		panic("ruzone: reading embedded cidrs.txt.gz: " + err.Error())
	}
	return strings.Fields(string(raw))
}
