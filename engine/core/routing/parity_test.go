package routing

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"testing"
)

var updateParity = flag.Bool("update-parity", false, "rewrite testdata/rule-parity.json")

// parityInputs are rules the server (hikkari-control internal/presets)
// must judge as this normalizer does: it refuses to release a catalog the
// client would reject. The file this pins is copied into the server's
// testdata; regenerate with go test ./core/routing -run Parity -update-parity.
var parityInputs = map[string][]string{
	"domains": {
		"example.com", "Example.COM", "https://www.example.com/path?q=1", "*.example.com",
		".example.com", "example.com.", "example.com:443", "localhost", "1.2.3.4",
		"exa mple.com", "-bad.com", "xn--80ak6aa92e.com", "a..b.com", "user@example.com", "",
	},
	"ips": {
		"1.2.3.4", "1.2.3.4/32", "10.1.2.3/8", "10.0.0.0/8", "2001:db8::1", "2001:db8::/32",
		"[2001:db8::1]", "https://example.com", "example.com", "300.1.1.1", "1.2.3.4/33",
	},
	"processes": {
		"steam.exe", "Steam.exe", `C:\Program Files\Steam\steam.exe`, "/Applications/Discord.app/Contents/MacOS/Discord",
		`"steam.exe"`, " steam.exe ", "*.exe", "a|b", "dir/",
	},
	"folders": {
		"steamapps/common/", `C:\Games\steamapps\common\`, "/opt//games/", "steam", "/", "a/*/b", `"games/x"`,
	},
}

type parityCase struct {
	Category string `json:"category"`
	Value    string `json:"value"`
	Normal   string `json:"normal"`
	OK       bool   `json:"ok"`
}

func TestRuleParityFixture(t *testing.T) {
	var cases []parityCase
	for _, category := range []string{"domains", "ips", "processes", "folders"} {
		for _, v := range parityInputs[category] {
			normal, err := NormalizeValue(category, v)
			cases = append(cases, parityCase{category, v, normal, err == nil})
		}
	}
	got, err := json.MarshalIndent(cases, "", "\t")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	const path = "testdata/rule-parity.json"
	if *updateParity {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
	if string(got) != string(want) {
		t.Fatalf("%s is stale: the normalizer changed. Regenerate it with -update-parity and copy it to hikkari-control internal/presets/testdata", path)
	}
}
