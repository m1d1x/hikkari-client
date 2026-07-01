// Package geoip resolves a profile's host (IPv4 literal or domain name) to
// an ISO 3166-1 alpha-2 country code, lowercase, matching what the Flutter
// side's `country_flags` package expects.
//
// Resolution order:
//  1. local embedded IPv4 range → country database (data/ranges.csv.gz,
//     built from ipdeny.com's per-country zone files by ./gen);
//  2. if the host is a domain, resolve it via DNS first;
//  3. if the local database has no match, fall back to a network lookup
//     (ipapi.co) — best-effort, short timeout, never blocks the caller for
//     long.
//
// A host's country can't change without its IP changing too, so a successful
// resolution is cached forever (in memory, and persisted to disk via
// [Load]/[Save] so it survives restarts) — the host's country is looked up
// again only if the profile's endpoint itself changes. Failed resolutions
// (DNS/network hiccup) are cached only briefly, so a transient blip doesn't
// stick forever but repeated calls also don't hammer the network.
package geoip

import (
	"bufio"
	"compress/gzip"
	"context"
	_ "embed"
	"encoding/csv"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed data/ranges.csv.gz
var rangesGz []byte

type ipRange struct {
	start uint32
	end   uint32
	code  string
}

var (
	dbOnce   sync.Once
	dbRanges []ipRange
)

func loadDB() {
	dbOnce.Do(func() {
		gz, err := gzip.NewReader(strings.NewReader(string(rangesGz)))
		if err != nil {
			return
		}
		defer gz.Close()
		r := csv.NewReader(bufio.NewReader(io.Reader(gz)))
		r.ReuseRecord = true
		for {
			rec, err := r.Read()
			if err != nil {
				break
			}
			if len(rec) != 3 {
				continue
			}
			start, err1 := strconv.ParseUint(rec[0], 10, 32)
			end, err2 := strconv.ParseUint(rec[1], 10, 32)
			if err1 != nil || err2 != nil {
				continue
			}
			dbRanges = append(dbRanges, ipRange{
				start: uint32(start),
				end:   uint32(end),
				code:  rec[2],
			})
		}
		// gen already sorts by start, but don't trust the embedded asset blindly.
		sort.Slice(dbRanges, func(i, j int) bool { return dbRanges[i].start < dbRanges[j].start })
	})
}

func ipToUint32(ip net.IP) (uint32, bool) {
	v4 := ip.To4()
	if v4 == nil {
		return 0, false
	}
	return uint32(v4[0])<<24 | uint32(v4[1])<<16 | uint32(v4[2])<<8 | uint32(v4[3]), true
}

// LookupIP returns the lowercase country code for ip using the local
// embedded database, or ("", false) if ip is IPv6 or falls outside every
// known range.
func LookupIP(ip net.IP) (code string, ok bool) {
	loadDB()
	v, ok := ipToUint32(ip)
	if !ok {
		return "", false
	}
	// Binary search for the last range whose start <= v, then check it
	// actually covers v (ranges don't tile the whole space — gaps exist).
	i := sort.Search(len(dbRanges), func(i int) bool { return dbRanges[i].start > v })
	if i == 0 {
		return "", false
	}
	r := dbRanges[i-1]
	if v < r.start || v > r.end {
		return "", false
	}
	return r.code, true
}

const (
	dnsTimeout    = 1500 * time.Millisecond
	onlineTimeout = 2 * time.Second
	// failRetryAfter bounds how long an unresolved host ("" result — DNS
	// down, network unreachable, brand-new IP range) is remembered before
	// the next call retries it. Successful resolutions never expire.
	failRetryAfter = 10 * time.Minute
)

var (
	cacheMu    sync.Mutex
	cache      = map[string]string{}    // host -> country code, permanent
	failedAt   = map[string]time.Time{} // host -> last failed attempt
	cachePath  string
	cacheDirty bool
)

// Load reads a previously persisted cache from path (see [Save]). Safe to
// call with a path that doesn't exist yet (first run). Call once, before
// the first [Resolve], so already-known hosts don't get re-resolved after
// a restart.
func Load(path string) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	cachePath = path
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var onDisk map[string]string
	if json.Unmarshal(data, &onDisk) == nil {
		for host, code := range onDisk {
			cache[host] = code
		}
	}
}

// Save persists the current permanent cache to the path given to [Load].
// No-op if Load was never called or nothing changed since the last Save.
func Save() {
	cacheMu.Lock()
	if cachePath == "" || !cacheDirty {
		cacheMu.Unlock()
		return
	}
	snapshot := make(map[string]string, len(cache))
	for k, v := range cache {
		snapshot[k] = v
	}
	cacheDirty = false
	path := cachePath
	cacheMu.Unlock()

	data, err := json.Marshal(snapshot)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}

// Resolve returns the lowercase country code for host (an IPv4/IPv6 literal
// or a domain name), or "" if it can't be determined. Safe for concurrent
// use. A successful result is cached forever — the host's IP would have to
// change for its country to change, and that's a new profile endpoint, not
// a cache invalidation problem. A failed result is retried after
// [failRetryAfter] instead of being stuck as "unknown".
func Resolve(host string) string {
	if host == "" {
		return ""
	}

	cacheMu.Lock()
	if code, found := cache[host]; found {
		cacheMu.Unlock()
		return code
	}
	if last, found := failedAt[host]; found && time.Since(last) < failRetryAfter {
		cacheMu.Unlock()
		return ""
	}
	cacheMu.Unlock()

	code := resolveUncached(host)

	cacheMu.Lock()
	if code == "" {
		failedAt[host] = time.Now()
	} else {
		cache[host] = code
		delete(failedAt, host)
		cacheDirty = true
	}
	cacheMu.Unlock()

	return code
}

func resolveUncached(host string) string {
	ip := net.ParseIP(host)
	if ip == nil {
		ip = resolveDNS(host)
	}
	if ip == nil {
		return ""
	}
	if code, ok := LookupIP(ip); ok {
		return code
	}
	return resolveOnline(ip)
}

func resolveDNS(host string) net.IP {
	ctx, cancel := context.WithTimeout(context.Background(), dnsTimeout)
	defer cancel()
	var resolver net.Resolver
	addrs, err := resolver.LookupIPAddr(ctx, host)
	if err != nil || len(addrs) == 0 {
		return nil
	}
	for _, a := range addrs {
		if v4 := a.IP.To4(); v4 != nil {
			return v4
		}
	}
	return addrs[0].IP
}

// resolveOnline is a best-effort fallback for IPs the local database missed
// (freshly announced ranges, IPv6, etc). No API key, single free endpoint,
// short timeout — a slow/unreachable network never blocks Resolve for long.
func resolveOnline(ip net.IP) string {
	client := &http.Client{Timeout: onlineTimeout}
	resp, err := client.Get("https://ipapi.co/" + ip.String() + "/country/")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16))
	if err != nil {
		return ""
	}
	code := strings.ToLower(strings.TrimSpace(string(body)))
	if len(code) != 2 {
		return ""
	}
	return code
}
