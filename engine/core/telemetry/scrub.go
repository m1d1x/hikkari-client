package telemetry

import (
	"crypto/sha256"
	"encoding/hex"
	"net/netip"
	"regexp"
	"strings"
)

// Placeholders that replace whatever the scrubber removes. They are part of
// the contract (docs/TELEMETRY.md §7): the server and the "what is sent"
// screen show them as is.
const (
	phIP     = "<ip>"
	phHost   = "<host>"
	phURL    = "<url>"
	phSecret = "<secret>"
)

// ownHosts are the only hosts a scrubbed string may still name: ours and the
// public probe. Anything else a user can reach is theirs and stays private.
var ownHosts = []string{"hikkari.net", "cp.cloudflare.com"}

var (
	// Any URI: subscription links, vless://, https://… Matched first, so its
	// host, credentials and query never reach the narrower rules below.
	reURI = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.\-]{1,15}://[^\s"'<>]+`)
	// name=value or name: value where the name says it is a secret.
	reKeyValue = regexp.MustCompile(`(?i)\b(password|passwd|pass|pwd|token|secret|private[_-]?key|public[_-]?key|short[_-]?id|uuid|psk|auth|key|pin(?:sha256)?)(\s*[=:]\s*)("[^"]*"|[^\s,;&"]+)`)
	reUUID     = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	// IPv6 is found as a candidate and confirmed by netip, so "12:30:45" or
	// "fe:ed" in a message survive. Optional [brackets] and :port go with it.
	reIPv6 = regexp.MustCompile(`\[?\b[0-9A-Fa-f]{0,4}(?::[0-9A-Fa-f]{0,4}){2,7}(?:%[0-9A-Za-z]+)?\b\]?(?::\d{1,5})?`)
	reIPv4 = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}(?:/\d{1,2})?(?::\d{1,5})?\b`)
	// Keys and tokens: a long run of base64/hex alphabet holding both letters
	// and digits. Identifiers in code are shorter or lack digits.
	reToken = regexp.MustCompile(`[A-Za-z0-9_+/=\-]{24,}`)
	reHost  = regexp.MustCompile(`(?i)\b(?:[a-z0-9](?:[a-z0-9\-]{0,61}[a-z0-9])?\.)+([a-z]{2,24})\b(?::\d{1,5})?`)
	// Home directories name the user. The rest of the path is the app's own.
	reHome   = regexp.MustCompile(`(?i)(/Users/|/home/|[A-Z]:\\Users\\|[A-Z]:/Users/)[^/\\\s"']+`)
	reDigits = regexp.MustCompile(`\d+`)
)

// fileExt are "TLDs" that are really file names in stack lines and messages
// (main.go, config.json). Real domains with these endings are rare in our
// logs, and a missed one is still not an address the user connected to by IP.
var fileExt = map[string]bool{
	"go": true, "dart": true, "rs": true, "json": true, "log": true, "txt": true, "yaml": true, "yml": true,
	"exe": true, "dll": true, "dylib": true, "so": true, "app": true, "pkg": true, "zip": true, "gz": true,
	"plist": true, "swift": true, "cpp": true, "h": true, "c": true, "mm": true, "js": true, "ts": true,
	"html": true, "md": true, "sh": true, "ps1": true, "bat": true, "tmp": true, "old": true, "bak": true,
	"sock": true, "pipe": true, "lock": true, "db": true, "csv": true,
}

// Scrub removes everything docs/TELEMETRY.md §7 forbids from s: addresses,
// hosts, URLs, keys, tokens, credentials and the user's home directory.
// Everything an error message or a diagnostic log tail sends passes here
// before it is queued.
func Scrub(s string) string {
	if s == "" {
		return s
	}
	s = reURI.ReplaceAllStringFunc(s, scrubURI)
	s = reHome.ReplaceAllStringFunc(s, func(string) string { return "~" })
	s = reKeyValue.ReplaceAllString(s, "${1}${2}"+phSecret)
	s = reUUID.ReplaceAllString(s, phSecret)
	s = reIPv6.ReplaceAllStringFunc(s, func(m string) string {
		if isIPv6Literal(m) {
			return phIP
		}
		return m
	})
	s = reIPv4.ReplaceAllString(s, phIP)
	s = reToken.ReplaceAllStringFunc(s, func(m string) string {
		if hasLetterAndDigit(m) {
			return phSecret
		}
		return m
	})
	s = reHost.ReplaceAllStringFunc(s, scrubHost)
	return s
}

// scrubURI keeps the scheme and, for our own hosts, the host — so
// "https://update.hikkari.net/stable/macos" stays readable while a
// subscription token or anyone else's server never leaves.
func scrubURI(m string) string {
	scheme, rest, _ := strings.Cut(m, "://")
	host := rest
	if i := strings.IndexAny(host, "/?#"); i >= 0 {
		host = host[:i]
	}
	if at := strings.LastIndexByte(host, '@'); at >= 0 {
		host = host[at+1:]
	}
	hostname := host
	if h, _, ok := strings.Cut(strings.TrimPrefix(hostname, "["), "]"); ok {
		hostname = h
	} else if i := strings.LastIndexByte(hostname, ':'); i >= 0 && strings.Count(hostname, ":") == 1 {
		hostname = hostname[:i]
	}
	if !isOwnHost(hostname) {
		return strings.ToLower(scheme) + "://" + phURL
	}
	path := strings.TrimPrefix(rest, host)
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	// Our only secret path is the subscription token.
	// An already scrubbed token is outside the match ("<" ends it), so an
	// empty remainder means there is nothing left to hide.
	if i := strings.Index(path, "/sub/"); i >= 0 && len(path) > i+len("/sub/") {
		path = path[:i] + "/sub/" + phSecret
	}
	return strings.ToLower(scheme) + "://" + strings.ToLower(hostname) + path
}

func scrubHost(m string) string {
	name := m
	if i := strings.LastIndexByte(name, ':'); i >= 0 {
		name = name[:i]
	}
	tld := strings.ToLower(name[strings.LastIndexByte(name, '.')+1:])
	if fileExt[tld] || isOwnHost(name) {
		return m
	}
	return phHost
}

func isOwnHost(h string) bool {
	h = strings.ToLower(strings.TrimSuffix(h, "."))
	for _, own := range ownHosts {
		if h == own || strings.HasSuffix(h, "."+own) {
			return true
		}
	}
	return false
}

func isIPv6Literal(m string) bool {
	m = strings.TrimPrefix(m, "[")
	if i := strings.IndexByte(m, ']'); i >= 0 {
		m = m[:i]
	}
	if i := strings.IndexByte(m, '%'); i >= 0 {
		m = m[:i]
	}
	addr, err := netip.ParseAddr(m)
	if err == nil {
		return addr.Is6()
	}
	// "2001:db8::1:443" — an address with its port glued on by a careless
	// formatter. Drop the last group and try again.
	if i := strings.LastIndexByte(m, ':'); i > 0 {
		addr, err = netip.ParseAddr(m[:i])
		return err == nil && addr.Is6()
	}
	return false
}

func hasLetterAndDigit(s string) bool {
	var letter, digit bool
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digit = true
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			letter = true
		}
		if letter && digit {
			return true
		}
	}
	return false
}

// Fingerprint groups occurrences of one error: the code plus the scrubbed
// message with every number folded, so "retry 3 of 5" and "retry 4 of 5" are
// the same failure.
func Fingerprint(code, scrubbed string) string {
	norm := reDigits.ReplaceAllString(scrubbed, "#")
	sum := sha256.Sum256([]byte(code + "\x00" + norm))
	return hex.EncodeToString(sum[:8])
}
