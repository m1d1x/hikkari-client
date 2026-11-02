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
