package configfile

import (
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
)

// normalizeRouteValue validates and canonicalises one user-typed routing
// entry before it reaches routing.json.
//
// Why this exists: every value in RouteList is compiled verbatim into a
// sing-box route rule, and sing-box parses those rules at startup. A single
// unparseable entry is not a soft error there — the core exits with FATAL
// before it ever opens the TUN device, and the launchd supervisor restarts it
// once a second forever. From the app's side that looks like nothing
// happening at all. So the only safe place to reject bad input is here, at
// the point the user types it, where an error message can still explain
// itself.
//
// The categories match addToList: "domains", "ips", "processes", "folders".
func normalizeRouteValue(category, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("пустое значение")
	}
	switch category {
	case "ips":
		return normalizeIPValue(value)
	case "domains":
		return normalizeDomainValue(value)
	case "processes":
		return normalizeProcessValue(value)
	case "folders":
		return normalizeFolderValue(value)
	default:
		return "", fmt.Errorf("неизвестная категория: %s", category)
	}
}

// normalizeIPValue accepts either a bare address or a CIDR prefix and always
// returns prefix form, which is what ip_cidr expects. A bare address becomes
// a host prefix (/32 or /128) rather than being passed through: sing-box
// accepts both, but storing one shape keeps dedup honest.
func normalizeIPValue(value string) (string, error) {
	if p, err := netip.ParsePrefix(value); err == nil {
		return p.Masked().String(), nil
	}
	if a, err := netip.ParseAddr(value); err == nil {
		return netip.PrefixFrom(a, a.BitLen()).String(), nil
	}
	// The mistake worth naming: a URL or domain typed into the ip field.
	if host := hostFromURLish(value); host != "" && !isIPish(host) {
		return "", fmt.Errorf(
			"%q — это не ip: добавь %q во вкладку «домены»", value, host)
	}
	return "", fmt.Errorf(
		"%q — не ip и не подсеть; примеры: 1.2.3.4, 192.168.0.0/16, 2001:db8::/32",
		value)
}

// normalizeProcessValue reduces whatever the user pasted to the bare binary
// name, which is the only shape process_name can match: sing-box compares
// filepath.Base(processPath) against the rule's strings, so a full path in the
// list would never match anything. Pasting a path is the common case — the
// name a user can actually find for a windowless process comes from Task
// Manager's "Open file location" or from the properties dialog — so the
// directory part is stripped instead of rejected.
//
// Case is preserved on purpose: that comparison is case-sensitive, and we
// have no way to learn the real spelling of a binary that isn't running.
// Resolving the case against the live process list is the UI's job.
func normalizeProcessValue(value string) (string, error) {
	name := strings.Trim(value, `"'`)
	name = strings.TrimSpace(name)
	if i := strings.LastIndexAny(name, `\/`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("%q — не удалось извлечь имя приложения", value)
	}
	if strings.ContainsAny(name, `*?<>|"`) {
		return "", fmt.Errorf("%q — не похоже на имя приложения", value)
	}
	return name, nil
}

// normalizeFolderValue reduces a pasted path to the fragment form folders
// are stored in: forward slashes, no repeated or leading ones, no drive
// letter. A drive or a home directory would tie the rule to one machine,
// which is exactly what a fragment is there to avoid. At least one directory
// boundary is required: a bare word ("steam") would match any path that
// merely contains it, which routes far more than anyone meant to.
func normalizeFolderValue(value string) (string, error) {
	frag := strings.TrimSpace(strings.Trim(value, `"'`))
	frag = strings.ReplaceAll(frag, `\`, "/")
	if len(frag) >= 2 && frag[1] == ':' {
		frag = frag[2:] // drop a drive letter, "C:"
	}
	for strings.Contains(frag, "//") {
		frag = strings.ReplaceAll(frag, "//", "/")
	}
	frag = strings.TrimPrefix(frag, "/")
	if strings.Trim(frag, "/") == "" {
		return "", fmt.Errorf("%q — пустой путь", value)
	}
	if !strings.Contains(frag, "/") {
		return "", fmt.Errorf(
			"%q — нужен кусок пути со слешем, например %q", value, frag+"/")
	}
	if strings.ContainsAny(frag, `*?<>|"`) {
		return "", fmt.Errorf("%q — не похоже на путь", value)
	}
	return frag, nil
}

// folderRegexes compiles folder fragments into process_path_regex patterns:
// case-insensitive, since Windows paths are, with either slash matching a
// boundary, since sing-box sees backslashes on Windows, and anchored at the
// start of a path segment, so "common/" does not match "uncommon/". Every fragment is
// quoted, so no stored value can produce an invalid pattern — which would
// stop sing-box at startup like an unparseable ip_cidr does.
func folderRegexes(frags []string) []string {
	out := make([]string, len(frags))
	for i, f := range frags {
		parts := strings.Split(f, "/")
		for j, p := range parts {
			parts[j] = regexp.QuoteMeta(p)
		}
		out[i] = `(?i)(^|[\\/])` + strings.Join(parts, `[\\/]`)
	}
	return out
}

// normalizeDomainValue reduces whatever the user pasted to the bare host
// sing-box's domain_suffix matches against. A pasted link is the common case
// — the list is usually built by copying addresses out of a browser or a
// chat — so a scheme, a path, a port or a leading wildcard are stripped
// instead of rejected.
func normalizeDomainValue(value string) (string, error) {
	host := value
	if h := hostFromURLish(value); h != "" {
		host = h
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "*."), ".")
	host = strings.TrimPrefix(host, ".")
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return "", fmt.Errorf("%q — не удалось извлечь домен", value)
	}
	if isIPish(host) {
		return "", fmt.Errorf(
			"%q — это ip: добавь его во вкладку «ip»", host)
	}
	if strings.ContainsAny(host, " \t/\\?#@") {
		return "", fmt.Errorf("%q — не похоже на домен", value)
	}
	if !strings.Contains(host, ".") {
		return "", fmt.Errorf("%q — домен без точки, нужен вид example.com", host)
	}
	return host, nil
}

// hostFromURLish pulls the host out of anything URL-shaped ("https://x/y",
// "x.com/y", "x.com:443"), and returns "" when there is nothing to strip.
func hostFromURLish(value string) string {
	s := value
	if !strings.Contains(s, "://") && strings.ContainsAny(s, "/?#") {
		s = "//" + s // url.Parse needs an authority marker to see a host
	} else if !strings.Contains(s, "://") {
		return stripPort(s)
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return ""
	}
	return stripPort(u.Host)
}

// stripPort drops a trailing :port, leaving bracketless IPv6 and bare hosts
// alone. netip does the IPv6 disambiguation: "2001:db8::1" has colons but no
// port, "example.com:443" does.
func stripPort(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "[") {
		if i := strings.Index(s, "]"); i > 0 {
			return s[1:i]
		}
		return s
	}
	if _, err := netip.ParseAddr(s); err == nil {
		return s // bare IPv6, every colon belongs to the address
	}
	if i := strings.LastIndex(s, ":"); i > 0 {
		if _, err := netip.ParseAddr(s[:i]); err == nil || !strings.Contains(s[:i], ":") {
			return s[:i]
		}
	}
	return s
}

func isIPish(s string) bool {
	if _, err := netip.ParseAddr(s); err == nil {
		return true
	}
	_, err := netip.ParsePrefix(s)
	return err == nil
}

// validRouteValue reports whether an entry already sitting in routing.json is
// safe to compile into a rule. Used by sanitizeList to drop legacy garbage:
// a routing.json written before this validation existed can hold a value that
// kills the core on every start, and the user has no way to reach it from the
// UI once the tunnel never comes up.
func validRouteValue(category, value string) bool {
	_, err := normalizeRouteValue(category, value)
	return err == nil
}
