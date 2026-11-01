package logging

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeLog_ConfigURIs(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "vless URI",
			input:    "Connecting to vless://b8313e11-4b10-449e-b924-f7b049301931@198.51.100.1:443?security=reality#MyVlessServer now",
			expected: "Connecting to [REDACTED_URI] now",
		},
		{
			name:     "trojan URI",
			input:    "Selected config: trojan://secretpass123@203.0.113.10:443?sni=example.com#TrojanServer",
			expected: "Selected config: [REDACTED_URI]",
		},
		{
			name:     "ss URI",
			input:    "Proxy uri ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@198.51.100.2:8388#Shadowsocks loaded",
			expected: "Proxy uri [REDACTED_URI] loaded",
		},
		{
			name:     "vpn URI",
			input:    "Importing vpn://custom-profile-token-here for user",
			expected: "Importing [REDACTED_URI] for user",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeLog(tc.input)
			if got != tc.expected {
				t.Errorf("SanitizeLog() =\n%q\nwant:\n%q", got, tc.expected)
			}
		})
	}
}

func TestSanitizeLog_UUIDs(t *testing.T) {
	input := `{"inbounds": [{"settings": {"clients": [{"id": "b8313e11-4b10-449e-b924-f7b049301931"}]}}]}`
	got := SanitizeLog(input)
	if strings.Contains(got, "b8313e11-4b10-449e-b924-f7b049301931") {
		t.Fatalf("UUID was not redacted: %s", got)
	}
	if !strings.Contains(got, "[REDACTED_UUID]") {
		t.Fatalf("Missing [REDACTED_UUID] in %s", got)
	}
}

func TestSanitizeLog_WireGuardKeys(t *testing.T) {
	wgKey := "47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=" // 44 chars base64 ending in =
	pskKey := "YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWE="

	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "wireguard conf PrivateKey",
			input: "[Interface]\nPrivateKey = " + wgKey + "\nAddress = 10.0.0.2/32\n",
		},
		{
			name:  "wireguard conf PresharedKey",
			input: "[Peer]\nPublicKey = pubkey\nPresharedKey = " + pskKey + "\n",
		},
		{
			name:  "json private_key",
			input: `{"type": "wireguard", "private_key": "` + wgKey + `"}`,
		},
		{
			name:  "standalone key in log",
			input: "Generated ephemeral key: " + wgKey + " for handshake",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeLog(tc.input)
			if strings.Contains(got, wgKey) || strings.Contains(got, pskKey) {
				t.Fatalf("WireGuard key leaked in %s", got)
			}
			if !strings.Contains(got, "[REDACTED_KEY]") {
				t.Fatalf("Expected [REDACTED_KEY] placeholder in %s", got)
			}
		})
	}
}

func TestSanitizeLog_Tokens(t *testing.T) {
	bearerToken := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.tokenpayload123456"
	inviteToken := "inv-abcdef12345678"
	sessionToken := "sess-fedcba87654321"

	tests := []struct {
		name         string
		input        string
		secret       string
		mustContain  string
	}{
		{
			name:        "Authorization Bearer header",
			input:       "Authorization: Bearer " + bearerToken + " sent to api",
			secret:      bearerToken,
			mustContain: "Bearer [REDACTED_TOKEN]",
		},
		{
			name:        "Standalone invite token",
			input:       "User activated with token " + inviteToken + " successfully",
			secret:      inviteToken,
			mustContain: "[REDACTED_TOKEN]",
		},
		{
			name:        "Session token key value",
			input:       "session_token: " + sessionToken + ", created_at: 123",
			secret:      sessionToken,
			mustContain: "session_token: [REDACTED_TOKEN]",
		},
		{
			name:        "Query param token",
			input:       "GET /api/v1/client/subscribe?token=secretquerytoken12345 HTTP/1.1",
			secret:      "secretquerytoken12345",
			mustContain: "?token=[REDACTED_TOKEN]",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeLog(tc.input)
			if strings.Contains(got, tc.secret) {
				t.Fatalf("Secret %q leaked in sanitized output: %s", tc.secret, got)
			}
			if !strings.Contains(got, tc.mustContain) {
				t.Fatalf("Expected %q in sanitized output, got: %s", tc.mustContain, got)
			}
		})
	}
}

func TestSanitizeLog_IPAddresses(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		mustContain string
		mustNotHave string
	}{
		{
			name:        "IPv4 public redacted",
			input:       "Dial tcp 198.51.100.42:443 -> connected",
			mustContain: "[REDACTED_IP]:443",
			mustNotHave: "198.51.100.42",
		},
		{
			name:        "IPv4 private redacted",
			input:       "Local peer 192.168.1.150 bound",
			mustContain: "[REDACTED_IP]",
			mustNotHave: "192.168.1.150",
		},
		{
			name:        "IPv4 loopback 127.0.0.1 preserved",
			input:       "Listening on 127.0.0.1:9090 for core api",
			mustContain: "127.0.0.1:9090",
			mustNotHave: "[REDACTED_IP]",
		},
		{
			name:        "IPv6 public redacted",
			input:       "Route to 2001:db8:85a3::8a2e:370:7334 via tun0",
			mustContain: "[REDACTED_IP]",
			mustNotHave: "2001:db8:85a3::8a2e:370:7334",
		},
		{
			name:        "IPv6 bracketed with port redacted",
			input:       "Connected to [2606:4700:4700::1111]:443 successfully",
			mustContain: "[REDACTED_IP]:443",
			mustNotHave: "2606:4700:4700::1111",
		},
		{
			name:        "IPv6 loopback ::1 preserved",
			input:       "Control socket bound to [::1]:8080",
			mustContain: "[::1]:8080",
			mustNotHave: "[REDACTED_IP]",
		},
		{
			name:        "Version numbers not redacted as IP",
			input:       "Version 1.0.10 initialized with build 2026",
			mustContain: "1.0.10",
			mustNotHave: "[REDACTED_IP]",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeLog(tc.input)
			if tc.mustNotHave != "" && strings.Contains(got, tc.mustNotHave) {
				t.Fatalf("Found forbidden %q in %s", tc.mustNotHave, got)
			}
			if tc.mustContain != "" && !strings.Contains(got, tc.mustContain) {
				t.Fatalf("Expected %q in %s", tc.mustContain, got)
			}
		})
	}
}

func TestSanitizeLog_RealisticFullLog(t *testing.T) {
	log := `2026/11/01 02:45:10 [sing-box] outbound/vless[0]: dial tcp 203.0.113.88:443: connected
2026/11/01 02:45:10 [api] auth with token=inv-secrettoken998877 using Bearer eyJhbGciOiJIUzI1NiJ9.secretpayload
2026/11/01 02:45:11 [engine] loading vless://9f8a3291-76f5-4dc2-bcf8-f865f3775b11@198.51.100.5:443?security=reality#EU
2026/11/01 02:45:12 [wireguard] peer key YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWE= connected on [2001:db8::1]:51820
2026/11/01 02:45:13 [http] core listening at 127.0.0.1:9090`

	sanitized := SanitizeLog(log)

	// Leaked checks
	forbidden := []string{
		"203.0.113.88",
		"inv-secrettoken998877",
		"eyJhbGciOiJIUzI1NiJ9.secretpayload",
		"9f8a3291-76f5-4dc2-bcf8-f865f3775b11",
		"198.51.100.5",
		"YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWE=",
		"2001:db8::1",
	}

	for _, f := range forbidden {
		if strings.Contains(sanitized, f) {
			t.Errorf("Secret leaked: %q found in sanitized output:\n%s", f, sanitized)
		}
	}

	// Must preserve 127.0.0.1
	if !strings.Contains(sanitized, "127.0.0.1:9090") {
		t.Errorf("127.0.0.1:9090 was mistakenly modified:\n%s", sanitized)
	}

	// Check placeholders
	expectedPlaceholders := []string{
		"[REDACTED_IP]",
		"[REDACTED_TOKEN]",
		"[REDACTED_URI]",
		"[REDACTED_KEY]",
	}

	for _, p := range expectedPlaceholders {
		if !strings.Contains(sanitized, p) {
			t.Errorf("Expected placeholder %q missing in output:\n%s", p, sanitized)
		}
	}
}

func TestCollectDiagnostics(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	logger := New(logFile)
	defer logger.Close()

	logger.Logf("Core started on 127.0.0.1:9090")
	logger.Logf("Connecting to 198.51.100.77:443 with vless://7d2a58b8-6228-48b4-93e5-8f6735e5d179@198.51.100.77:443")
	logger.Logf("User token: inv-abcdef9988776655")

	AppVersion = "1.0.10"
	diag := logger.CollectDiagnostics(10)

	if !strings.Contains(diag, "=== Hikkari Diagnostics Report ===") {
		t.Errorf("Missing diagnostics header in %s", diag)
	}
	if !strings.Contains(diag, "App Version: 1.0.10") {
		t.Errorf("Missing app version in %s", diag)
	}
	if !strings.Contains(diag, "127.0.0.1:9090") {
		t.Errorf("127.0.0.1 was not preserved in diagnostics:\n%s", diag)
	}
	if strings.Contains(diag, "198.51.100.77") || strings.Contains(diag, "7d2a58b8-6228-48b4-93e5-8f6735e5d179") || strings.Contains(diag, "inv-abcdef9988776655") {
		t.Fatalf("Diagnostics leaked secret data:\n%s", diag)
	}
}

func TestSanitizeLogHidesHomeDirectory(t *testing.T) {
	saved := home
	home = "/Users/alice"
	t.Cleanup(func() { home = saved })
	got := SanitizeLog("process=/Users/alice/Applications/Discord.app/Contents/MacOS/Discord")
	if got != "process=~/Applications/Discord.app/Contents/MacOS/Discord" {
		t.Fatalf("got %q", got)
	}
}
