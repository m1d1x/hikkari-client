package singbox

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"NekkoVPN/core/profile"
)

func amneziaLink(t *testing.T, doc map[string]interface{}) string {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return "vpn://" + base64.RawURLEncoding.EncodeToString(raw)
}

// A newer Amnezia server names its container amnezia-awg2. The AWG 2.0 fields
// reach the endpoint: S3, S4, H ranges, I1, none of them lost on the way.
func TestAmneziaAwg2ContainerIsAmneziaWG(t *testing.T) {
	conf := "[Interface]\nPrivateKey = cHJpdmF0ZQ==\nAddress = 10.8.1.2/32\n" +
		"Jc = 4\nJmin = 10\nJmax = 50\nS1 = 20\nS2 = 30\nS3 = 40\nS4 = 50\n" +
		"H1 = 100-200\nH2 = 300-400\nH3 = 500-600\nH4 = 700-800\nI1 = <b 0xc0><r 16>\n\n" +
		"[Peer]\nPublicKey = cHVibGlj\nEndpoint = 203.0.113.5:40001\nAllowedIPs = 0.0.0.0/0\n"
	d, err := ParseURI(amneziaLink(t, map[string]interface{}{
		"description":      "мой сервер",
		"hostName":         "203.0.113.5",
		"defaultContainer": "amnezia-awg2",
		"containers": []interface{}{map[string]interface{}{
			"container": "amnezia-awg2",
			"awg":       map[string]interface{}{"last_config": conf},
		}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if d.Kind != profile.KindEndpoint || d.Label != "мой сервер" || d.Config["type"] != "awg" {
		t.Fatalf("draft %+v", d)
	}
	for k, want := range map[string]interface{}{"s3": 40, "s4": 50, "h1": "100-200", "h4": "700-800", "i1": "<b 0xc0><r 16>"} {
		if d.Config[k] != want {
			t.Errorf("%s = %v, want %v", k, d.Config[k], want)
		}
	}
}

// What each kind of link holds no server for gets said in words a person can act
// on. The field names reach the message and so the log, the values never do.
func TestAmneziaLinkWithoutAContainerSaysWhatItIs(t *testing.T) {
	const secret = "s3cr3t-value"
	for _, c := range []struct {
		name string
		doc  map[string]interface{}
		want string
	}{
		{"free", map[string]interface{}{"api_key": secret, "api_endpoint": secret, "description": secret}, "ключ сервиса Amnezia"},
		{"premium", map[string]interface{}{"auth_data": map[string]interface{}{"api_key": secret}, "containers": []interface{}{}}, "ключ сервиса Amnezia"},
		{"admin", map[string]interface{}{"hostName": secret, "userName": secret, "password": secret}, "ключ администратора"},
		{"other", map[string]interface{}{"something": secret}, "не найден поддерживаемый контейнер"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseURI(amneziaLink(t, c.doc))
			if err == nil {
				t.Fatal("parsed")
			}
			msg := err.Error()
			if !strings.Contains(msg, c.want) {
				t.Errorf("message %q lacks %q", msg, c.want)
			}
			if strings.Contains(msg, secret) {
				t.Errorf("a value reached the message: %q", msg)
			}
			for k := range c.doc {
				if !strings.Contains(msg, k) {
					t.Errorf("field %q is not named: %q", k, msg)
				}
			}
		})
	}
}

func TestAmneziaLinkShapeIsBoundedAndDropsOddNames(t *testing.T) {
	doc := map[string]interface{}{"ok_name": 1, "with space": 1, "<script>": 1}
	for i := 0; i < 30; i++ {
		doc[strings.Repeat("a", i+1)] = 1
	}
	got := amneziaLinkShape(doc)
	if strings.Contains(got, "script") || strings.Contains(got, "with space") {
		t.Errorf("odd name kept: %q", got)
	}
	if n := len(strings.Split(got, ", ")); n > 12 {
		t.Errorf("%d names", n)
	}
	if amneziaLinkShape(map[string]interface{}{}) != "нет" {
		t.Error("empty link")
	}
}

func awgConfWith(extra string) string {
	return "[Interface]\nPrivateKey = cHJpdmF0ZQ==\nAddress = 10.66.0.7/32\nJc = 4\nJmin = 40\nJmax = 70\nS1 = 20\nS2 = 30\n" +
		"H1 = 100-200\nH2 = 300-400\nH3 = 500-600\nH4 = 700-800\nI1 = <r 8>\n" + extra +
		"\n[Peer]\nPublicKey = cHVibGlj\nEndpoint = 203.0.113.5:40001\nAllowedIPs = 0.0.0.0/0\n"
}

// The hub's link carries both a fixed I1, for released clients, and a request for
// the live generator. The core refuses the two together, so I1 must go.
func TestAwgMasqueradeReplacesTheFixedDecoy(t *testing.T) {
	d, err := parseAmneziaWGIni(awgConfWith("Ip = quic\nId = www.gstatic.com\nIb = chrome\n"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Config["ip"] != "quic" || d.Config["id"] != "www.gstatic.com" || d.Config["ib"] != "chrome" {
		t.Fatalf("%v", d.Config)
	}
	if _, ok := d.Config["i1"]; ok {
		t.Fatal("i1 stayed beside the generator: the core refuses that")
	}
}

// A link comes from anywhere, and the core refuses a whole config over a value it
// doesn't know. A bad request is dropped and the link keeps working on its I1.
func TestAwgMasqueradeIgnoresWhatTheCoreWouldRefuse(t *testing.T) {
	for _, extra := range []string{
		"", "Ip = dns\nId = a.example\n", "Ip = quic\n", "Ip = quic\nId = not a domain\n",
		"Ip = quic\nId = a.example\nIb = chrome-full\n",
	} {
		d, err := parseAmneziaWGIni(awgConfWith(extra))
		if err != nil {
			t.Fatal(err)
		}
		if d.Config["i1"] != "<r 8>" || d.Config["ip"] != nil || d.Config["id"] != nil || d.Config["ib"] != nil {
			t.Errorf("%q: %v", extra, d.Config)
		}
	}
}
