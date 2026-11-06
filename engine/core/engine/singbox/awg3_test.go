package singbox

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	configfile "NekkoVPN/core/engine/singbox/config"
	"NekkoVPN/core/profile/legacy"
)

const awg3HeaderKey = "pBNbQ3vIFMTZCbCkQtLObNUtsM9hAi+ZhlrgZbH0Xgc="

// The 3.1 profile of the 06.11 stand (awgl1): fixed headers, S1..S4 long
// enough for the header cipher nonce, every 3.x knob set.
const awg31Lines = "HeaderProtectionKey = " + awg3HeaderKey + "\n" +
	"ContentPaddingAddition = 10-50\nRekeyAfterTime = 100-120\nRekeyTimeout = 5-7\n" +
	"RejectAfterTime = 170-190\nKeepaliveTimeout = 10-15\nMaxHandshakeAttempts = 15-20\n" +
	"RandomTrailers = 1\nDisableCookies = 1\n"

// awg31Want is what the profile, and the endpoint the core reads, must hold.
var awg31Want = map[string]interface{}{
	"header_protection_key":    awg3HeaderKey,
	"content_padding_addition": "10-50",
	"rekey_after_time":         "100-120",
	"rekey_timeout":            "5-7",
	"reject_after_time":        "170-190",
	"keepalive_timeout":        "10-15",
	"max_handshake_attempts":   "15-20",
	"random_trailers":          true,
	"disable_cookies":          true,
}

func awg31Conf(iface string) string {
	return "[Interface]\nPrivateKey = 6Mf9KmZ3Nn4bq2oXQyQbF0jv3bB4i0cVZ0Xh1q3xXkE=\nAddress = 10.66.0.7/32\nMTU = 1280\n" +
		"Jc = 4\nJmin = 40\nJmax = 70\nS1 = 16\nS2 = 16\nS3 = 16\nS4 = 16\nH1 = 1\nH2 = 2\nH3 = 3\nH4 = 4\n" + iface +
		"\n[Peer]\nPublicKey = lTmjpHaGYBYQ8T5oogAWheEzhjCnzFwvoaZYQevHMgQ=\nEndpoint = 203.0.113.9:443\nAllowedIPs = 0.0.0.0/0\nPersistentKeepalive = 20-25\n"
}

func assertAWG31(t *testing.T, where string, cfg map[string]interface{}) {
	t.Helper()
	for k, want := range awg31Want {
		if got := cfg[k]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %s = %#v, want %#v", where, k, got, want)
		}
	}
	peers, _ := cfg["peers"].([]interface{})
	if len(peers) != 1 {
		t.Fatalf("%s: peers %v", where, cfg["peers"])
	}
	if ka := peers[0].(map[string]interface{})["persistent_keepalive_interval"]; ka != "20-25" {
		t.Errorf("%s: persistent_keepalive_interval = %#v, want the 3.x range", where, ka)
	}
}

// A 3.1 profile keeps every 3.x field the whole way: import, the profile
// store on disk, the share link, a re-import of that link, and the endpoint
// the core is started with, which the real core accepts.
func TestAWG31ProfileKeepsEveryFieldEndToEnd(t *testing.T) {
	d, err := ParseURI(awg31Conf(awg31Lines))
	if err != nil {
		t.Fatal(err)
	}
	if d.Config["type"] != "awg" {
		t.Fatalf("type %v", d.Config["type"])
	}
	assertAWG31(t, "import", d.Config)

	store := NewStore(filepath.Join(t.TempDir(), "profiles.json"))
	name, err := store.Add("awg31", string(d.Kind), d.Config)
	if err != nil {
		t.Fatal(err)
	}
	stored, ok, err := store.Get(name)
	if err != nil || !ok {
		t.Fatalf("get: %v %v", ok, err)
	}
	assertAWG31(t, "store", stored.Config)

	link, err := ExportURI(stored)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(awg31Lines), "\n") {
		if !strings.Contains(link, "\n"+line+"\n") {
			t.Errorf("export lost %q", line)
		}
	}
	again, err := ParseURI(link)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again.Config, d.Config) {
		t.Fatalf("re-import differs:\n%v\n%v", again.Config, d.Config)
	}

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"outbounds":[{"type":"direct","tag":"direct"}],"route":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := map[string]interface{}{}
	for k, v := range stored.Config {
		cfg[k] = v
	}
	cfg["tag"] = "p-awg31"
	if err := configfile.New(path).RebuildActive([]configfile.ProfileSpec{{Tag: "p-awg31", Kind: "endpoint", Config: cfg}}, "p-awg31", false, nil); err != nil {
		t.Fatal(err)
	}
	compiled, err := configfile.New(path).Read()
	if err != nil {
		t.Fatal(err)
	}
	eps, _ := compiled["endpoints"].([]interface{})
	if len(eps) != 1 {
		t.Fatalf("endpoints %v", eps)
	}
	ep := eps[0].(map[string]interface{})
	if ep["type"] != "wireguard" {
		t.Fatalf("core endpoint type %v", ep["type"])
	}
	assertAWG31(t, "compiled", ep)
	realCoreCheck(t, ep, compiled["dns"])
}

// realCoreCheck runs the bundled sing-box-lx core's own check on the endpoint,
// with the compiled DNS servers its destination resolver names.
func realCoreCheck(t *testing.T, ep map[string]interface{}, dns interface{}) {
	t.Helper()
	core, err := filepath.Abs("../../../../archive/sing-box")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(core); err != nil {
		t.Log("no sing-box binary at archive/sing-box: real core check skipped")
		return
	}
	var servers []interface{}
	if d, ok := dns.(map[string]interface{}); ok {
		for _, s := range d["servers"].([]interface{}) {
			if tag, _ := s.(map[string]interface{})["tag"].(string); strings.HasPrefix(tag, "dns-in-") {
				servers = append(servers, s)
			}
		}
	}
	raw, err := json.Marshal(map[string]interface{}{
		"dns":       map[string]interface{}{"servers": servers},
		"endpoints": []interface{}{ep},
		"outbounds": []interface{}{map[string]interface{}{"type": "direct", "tag": "direct"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "core.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(core, "check", "-c", path).CombinedOutput(); err != nil {
		t.Fatalf("sing-box check: %v\n%s", err, out)
	}
}

// An Amnezia container may carry the 3.x fields next to the .conf, in its awg
// block or in last_config, by .conf name or by the profile's name, switches as
// JSON booleans; each reaches the profile.
func TestAWG31ContainerFieldsReachTheProfile(t *testing.T) {
	fields := map[string]interface{}{}
	snake := map[string]interface{}{}
	for _, f := range legacy.AWG3Fields {
		v := awg31Want[f.Key]
		snake[f.Key] = v
		if b, ok := v.(bool); ok && b {
			v = "1"
		}
		fields[f.Ini] = v
	}
	conf := awg31Conf("")
	for name, container := range map[string]map[string]interface{}{
		"container": merge(map[string]interface{}{"config": conf}, fields),
		"awg block": {"awg": merge(map[string]interface{}{"last_config": conf}, snake)},
		"last_config": {"last_config": func() string {
			raw, _ := json.Marshal(merge(map[string]interface{}{"config": conf}, fields))
			return string(raw)
		}()},
		"ini": {"config": awg31Conf(awg31Lines)},
	} {
		t.Run(name, func(t *testing.T) {
			container["container"] = "amnezia-awg2"
			d, err := ParseURI(amneziaLink(t, map[string]interface{}{"containers": []interface{}{container}}))
			if err != nil {
				t.Fatal(err)
			}
			assertAWG31(t, name, d.Config)
		})
	}
}

func merge(a, b map[string]interface{}) map[string]interface{} {
	for k, v := range b {
		a[k] = v
	}
	return a
}

// Header protection without junk packets is still AmneziaWG, never plain
// WireGuard, which the server would not answer.
func TestAWG3WithoutJunkIsStillAmneziaWG(t *testing.T) {
	conf := strings.ReplaceAll(awg31Conf("HeaderProtectionKey = "+awg3HeaderKey+"\n"), "Jc = 4\n", "")
	d, err := ParseURI(conf)
	if err != nil {
		t.Fatal(err)
	}
	if d.Config["type"] != "awg" || d.Config["header_protection_key"] != awg3HeaderKey {
		t.Fatalf("got %v", d.Config)
	}
}

// A 3.x value the core would not take is refused by name instead of being
// dropped, and the header protection key never shows up in the error.
func TestAWG3BadValueIsRefusedNotDropped(t *testing.T) {
	for line, field := range map[string]string{
		"HeaderProtectionKey = c2VjcmV0LWtleQ==": "HeaderProtectionKey",
		"ContentPaddingAddition = 50-10":         "ContentPaddingAddition",
		"RekeyAfterTime = soon":                  "RekeyAfterTime",
		"MaxHandshakeAttempts = 1-2-3":           "MaxHandshakeAttempts",
		"RandomTrailers = maybe":                 "RandomTrailers",
		"DisableCookies = 2":                     "DisableCookies",
		"KeepaliveTimeout = 99999999999":         "KeepaliveTimeout",
	} {
		_, err := ParseURI(awg31Conf(line + "\n"))
		if err == nil || !strings.Contains(err.Error(), field) || strings.Contains(err.Error(), "c2VjcmV0LWtleQ") {
			t.Errorf("%s: %v", line, err)
		}
	}
}

// Switches that are off and zero ranges are the defaults: the profile stays
// as a 2.0 one would, and nothing is lost by leaving them out.
func TestAWG3DefaultsAreNotStored(t *testing.T) {
	d, err := ParseURI(awg31Conf("RandomTrailers = 0\nDisableCookies = false\nRekeyTimeout = 0\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range legacy.AWG3Fields {
		if v, ok := d.Config[f.Key]; ok {
			t.Errorf("%s stored as %v", f.Key, v)
		}
	}
}
