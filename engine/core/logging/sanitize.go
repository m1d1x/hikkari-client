package logging

import (
	"fmt"
	"net"
	"net/netip"
	"os"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// AppVersion can be stamped at build or set at runtime for diagnostic reports.
var AppVersion = "dev"

var (
	// Config URIs: vless://, trojan://, vpn://, ss://
	reConfigURI = regexp.MustCompile(`(?i)\b(?:vless|vmess|trojan|tuic|hysteria2|hy2|vpn|ss)://[^\s"'<>]+`)

	// Labeled private keys (WireGuard / AmneziaWG)
	reLabeledKey = regexp.MustCompile(`(?i)\b(private[_-]?key|preshared[_-]?key|psk)(\s*[:=]\s*["']?)[A-Za-z0-9+/_-]{43,44}={0,2}(["']?)`)

	// Standalone WireGuard / AmneziaWG 32-byte base64 keys (44 chars ending in =)
	reWGKeyCandidate = regexp.MustCompile(`\b[A-Za-z0-9+/_-]{43}={1,2}`)

	// UUID (8-4-4-4-12 hex) for VLESS/Trojan keys
	reUUID = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)

	// Authorization Bearer
	reBearer = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9\-._~+/]+=*`)

	// Key-value tokens: invite_token, session_token, token, access_token, etc.
	reTokenKV = regexp.MustCompile(`(?i)\b(invite[-_]?token|session[-_]?(?:token|id)?|auth[-_]?token|access[-_]?token|token)(\s*[:=]\s*["']?)[A-Za-z0-9\-._~+/]{8,}=*(["']?)`)

	// Query params for tokens
	reTokenQuery = regexp.MustCompile(`(?i)([?&](?:invite|invite_token|token|session|session_token|auth|access_token)=)[A-Za-z0-9\-._~+/]+=*`)

	// Standalone invite or session tokens (e.g. inv-xxxx, sess-xxxx)
	reStandaloneToken = regexp.MustCompile(`(?i)\b(?:inv|invite|sess|session)-[A-Za-z0-9_\-]{8,}\b`)

	// IPv6 candidates (with optional brackets and port)
	reIPv6Candidate = regexp.MustCompile(`\[?[0-9a-fA-F]{0,4}(?::[0-9a-fA-F]{0,4}){2,7}(?:%[0-9a-zA-Z]+)?\]?(?::\d{1,5})?`)

	// IPv4 pattern
	reIPv4 = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
)

// home is the user's home directory, replaced with "~" in sanitized text.
var home, _ = os.UserHomeDir()

// SanitizeLog strips sensitive information from log text:
// - Config URIs (vless://, vmess://, trojan://, tuic://, hysteria2://, vpn://, ss://) -> [REDACTED_URI]
// - WireGuard / AmneziaWG private keys -> [REDACTED_KEY]
// - VLESS / Trojan UUIDs -> [REDACTED_UUID]
// - Invite tokens, session tokens, Authorization Bearer -> [REDACTED_TOKEN]
// - IPv4 and IPv6 addresses (except loopback 127.0.0.1 / ::1) -> [REDACTED_IP]
func SanitizeLog(text string) string {
	if text == "" {
		return ""
	}

	// 0. Home directory: process and config paths carry the user name.
	if home != "" {
		text = strings.ReplaceAll(text, home, "~")
	}

	// 1. Config URIs
	text = reConfigURI.ReplaceAllString(text, "[REDACTED_URI]")

	// 2. WireGuard / AmneziaWG keys
	text = reLabeledKey.ReplaceAllString(text, "${1}${2}[REDACTED_KEY]${3}")
	text = reWGKeyCandidate.ReplaceAllStringFunc(text, func(m string) string {
		if len(m) == 44 && strings.HasSuffix(m, "=") {
			return "[REDACTED_KEY]"
		}
		return m
	})

	// 3. UUID keys (VLESS/Trojan)
	text = reUUID.ReplaceAllString(text, "[REDACTED_UUID]")

	// 4. Tokens
	text = reBearer.ReplaceAllString(text, "Bearer [REDACTED_TOKEN]")
	text = reTokenKV.ReplaceAllString(text, "${1}${2}[REDACTED_TOKEN]${3}")
	text = reTokenQuery.ReplaceAllString(text, "${1}[REDACTED_TOKEN]")
	text = reStandaloneToken.ReplaceAllString(text, "[REDACTED_TOKEN]")

	// 5. IPv6 addresses
	text = reIPv6Candidate.ReplaceAllStringFunc(text, func(m string) string {
		candidate := m
		portSuffix := ""

		// Check for bracketed IPv6: [fe80::1]:8080 or [fe80::1]
		if strings.HasPrefix(candidate, "[") {
			if idx := strings.Index(candidate, "]"); idx != -1 {
				portSuffix = candidate[idx+1:]
				candidate = candidate[1:idx]
			}
		} else if strings.Count(candidate, ":") > 1 {
			// Might have trailing :port if last segment is a port
			if lastColon := strings.LastIndex(candidate, ":"); lastColon != -1 {
				possiblePort := candidate[lastColon+1:]
				if _, err := net.LookupPort("tcp", possiblePort); err == nil && !strings.Contains(candidate[:lastColon], "::") {
					// could be port, but let netip.ParseAddr verify candidate first
				}
			}
		}

		// Strip zone identifier if present
		if zoneIdx := strings.Index(candidate, "%"); zoneIdx != -1 {
			candidate = candidate[:zoneIdx]
		}

		addr, err := netip.ParseAddr(candidate)
		if err != nil || !addr.Is6() {
			return m
		}

		// Preserve IPv6 loopback (::1)
		if addr.IsLoopback() {
			return m
		}

		if portSuffix != "" {
			return "[REDACTED_IP]" + portSuffix
		}
		return "[REDACTED_IP]"
	})

	// 6. IPv4 addresses
	text = reIPv4.ReplaceAllStringFunc(text, func(m string) string {
		ip := net.ParseIP(m)
		if ip == nil || ip.To4() == nil {
			return m
		}

		// Loopback 127.0.0.1 (or 127.0.0.0/8) is preserved
		if ip.String() == "127.0.0.1" || ip.IsLoopback() {
			return m
		}

		return "[REDACTED_IP]"
	})

	return text
}

// CollectDiagnostics collects the last maxLines from the active log file,
// runs them through SanitizeLog, and adds metadata (OS, arch, app version, time).
func (l *Logger) CollectDiagnostics(maxLines int) string {
	if maxLines <= 0 {
		maxLines = 300
	}

	rawLogs := strings.Join(l.TailLines(maxLines), "\n")

	sanitized := SanitizeLog(rawLogs)

	osName := runtime.GOOS
	if osName == "darwin" {
		osName = "macos"
	}

	var sb strings.Builder
	sb.WriteString("=== Hikkari Diagnostics Report ===\n")
	sb.WriteString(fmt.Sprintf("OS: %s\n", osName))
	sb.WriteString(fmt.Sprintf("Arch: %s\n", runtime.GOARCH))
	sb.WriteString(fmt.Sprintf("App Version: %s\n", AppVersion))
	sb.WriteString(fmt.Sprintf("Timestamp: %s\n", time.Now().UTC().Format(time.RFC3339)))
	sb.WriteString("==================================\n")
	if sanitized != "" {
		sb.WriteString(sanitized)
		sb.WriteString("\n")
	}

	return sb.String()
}
