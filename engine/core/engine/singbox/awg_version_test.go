package singbox

import (
	"encoding/json"
	"strings"
	"testing"

	"NekkoVPN/core/profile"
	"NekkoVPN/core/profile/legacy"
)

func TestAWG3ImportCannotSilentlyDropProtocolFields(t *testing.T) {
	for _, field := range []string{
		"HeaderProtectionKey", "ContentPaddingAddition", "RekeyAfterTime",
		"RekeyTimeout", "RejectAfterTime", "KeepaliveTimeout",
		"MaxHandshakeAttempts", "RandomTrailers", "DisableCookies",
	} {
		t.Run(field, func(t *testing.T) {
			const secret = "must-not-appear-in-error"
			ini := awgConfWith(field + " = " + secret + "\n")
			assertRejected := func(err error) {
				t.Helper()
				if err == nil || !strings.Contains(err.Error(), "AmneziaWG 3.x") || strings.Contains(err.Error(), secret) {
					t.Fatalf("expected an explicit, secret-free version refusal, got %v", err)
				}
			}
			_, err := ParseURI(ini)
			assertRejected(err)
			// Header protection alone must not fall back to plain WireGuard.
			_, err = ParseURI(strings.ReplaceAll(ini, "Jc = 4\n", ""))
			assertRejected(err)
			for _, location := range []string{"container", "awg", "last_config", "ini"} {
				container := map[string]interface{}{"container": "amnezia-awg", "config": awgConfWith("")}
				switch location {
				case "container":
					container[field] = secret
				case "awg":
					container["awg"] = map[string]interface{}{field: secret}
				case "last_config":
					last, _ := json.Marshal(map[string]interface{}{field: secret, "config": awgConfWith("")})
					container["last_config"] = string(last)
				case "ini":
					container["config"] = ini
				}
				_, err = ParseURI(amneziaLink(t, map[string]interface{}{"containers": []interface{}{container}}))
				assertRejected(err)
			}
		})
	}
}

func TestAWG3NativeProfileCannotBeExportedAsAWG2(t *testing.T) {
	for _, field := range []string{
		"header_protection_key", "content_padding_addition", "rekey_after_time",
		"rekey_timeout", "reject_after_time", "keepalive_timeout",
		"max_handshake_attempts", "random_trailers", "disable_cookies",
	} {
		t.Run(field, func(t *testing.T) {
			d, err := ParseURI(awgConfWith(""))
			if err != nil {
				t.Fatal(err)
			}
			d.Config[field] = false // Presence matters even when a switch is off.
			out, err := legacy.ExportURI(profile.Profile{Config: d.Config})
			if err == nil || out != "" || !strings.Contains(err.Error(), "AmneziaWG 3.x") {
				t.Fatalf("export silently downgraded %s: %q, %v", field, out, err)
			}
		})
	}
}
