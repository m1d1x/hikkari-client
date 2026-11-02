package configfile

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func awgProfile() map[string]interface{} {
	return map[string]interface{}{
		"tag": "p-awg", "type": "awg",
		"private_key": "6Mf9KmZ3Nn4bq2oXQyQbF0jv3bB4i0cVZ0Xh1q3xXkE=",
		"address":     []interface{}{"10.66.0.7/32"},
		"jc":          4, "jmin": 40, "jmax": 70, "s1": 33, "s2": 57,
		"h1": "11", "h2": "12", "h3": "13", "h4": "14",
		"peers": []interface{}{map[string]interface{}{
			"address": "203.0.113.9", "port": 41202,
			"public_key":  "lTmjpHaGYBYQ8T5oogAWheEzhjCnzFwvoaZYQevHMgQ=",
			"allowed_ips": []interface{}{"0.0.0.0/0"},
		}},
	}
}

// The sing-box-lx core has no "awg" endpoint type and refuses the whole config
// over one (seen on a live hub link, 02.11): every other server dies with it.
// Whichever way a config gets written, the core must read "wireguard", and the
// profile itself must keep saying AmneziaWG to the rest of the client.
func TestAwgProfileReachesTheCoreAsWireguard(t *testing.T) {
	profile := awgProfile()
	for name, write := range map[string]func(s *Store) error{
		"rebuild": func(s *Store) error {
			return s.RebuildActive([]ProfileSpec{{Tag: "p-awg", Kind: "endpoint", Config: profile}}, "p-awg", false, nil)
		},
		"apply": func(s *Store) error {
			return s.ApplyActive("endpoint", profile, "p-awg", map[string]bool{}, "proxy-out")
		},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(`{"outbounds":[{"type":"direct","tag":"direct"}],"route":{}}`), 0o644); err != nil {
				t.Fatal(err)
			}
			s := New(path)
			if err := write(s); err != nil {
				t.Fatal(err)
			}
			cfg, err := s.Read()
			if err != nil {
				t.Fatal(err)
			}
			eps, _ := cfg["endpoints"].([]interface{})
			if len(eps) != 1 {
				t.Fatalf("endpoints %v", eps)
			}
			ep := eps[0].(map[string]interface{})
			if ep["type"] != "wireguard" || ep["jc"] == nil || ep["h4"] != "14" {
				t.Fatalf("endpoint as written: %v", ep)
			}
			if profile["type"] != "awg" {
				t.Fatal("the stored profile was changed")
			}
		})
	}
}

// The real core takes the block: the type it wants and the fields it knows.
func TestAwgEndpointPassesTheRealCoresCheck(t *testing.T) {
	core, err := filepath.Abs("../../../../../archive/sing-box")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(core); err != nil {
		t.Skip("no sing-box binary at archive/sing-box")
	}
	raw, err := json.Marshal(map[string]interface{}{"endpoints": []interface{}{singBoxEndpoint(awgProfile())}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(core, "check", "-c", path).CombinedOutput(); err != nil {
		t.Fatalf("sing-box check: %v\n%s", err, out)
	}
}
