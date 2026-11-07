package diag

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// wireSNI is the name the recorded hellos carry; the line must never print it.
const wireSNI = "wire-mirror.example"

// wireCases are the outbounds testdata/hello was recorded from, one per
// fingerprint name the core takes, plus the shapes the fleet hands out.
func wireCases() map[string]map[string]interface{} {
	tlsWith := func(fp string, extra map[string]interface{}) map[string]interface{} {
		t := map[string]interface{}{"enabled": true, "server_name": wireSNI, "insecure": true}
		if fp != "" {
			t["utls"] = map[string]interface{}{"enabled": true, "fingerprint": fp}
		}
		for k, v := range extra {
			t[k] = v
		}
		return t
	}
	vless := func(tls map[string]interface{}) map[string]interface{} {
		return map[string]interface{}{
			"type": "vless", "tag": "p-rec", "server": "127.0.0.1", "server_port": 1,
			"uuid": "bf000d23-0451-4b9c-8a7c-1d2e3f405162", "tls": tls,
		}
	}
	cases := map[string]map[string]interface{}{}
	for _, fp := range []string{"chrome", "chrome_155", "firefox", "edge", "safari", "ios", "android", "360", "qq", "randomized"} {
		cases["tls-"+fp] = vless(tlsWith(fp, nil))
	}
	// Chrome shuffles its extensions: a second recording proves hk does not care.
	cases["tls-chrome-again"] = vless(tlsWith("chrome", nil))
	// No fingerprint: the core's crypto/tls hello (plan 2.1, row 2).
	cases["tls-go"] = vless(tlsWith("", nil))
	reality := func(fp, keyShare string) map[string]interface{} {
		r := map[string]interface{}{"enabled": true, "public_key": "jNXHt1yRo0vDuchQlIP6Z0ZvjT3KtzVI-T4E7RoLJS0", "short_id": "0123abcd"}
		if keyShare != "" {
			r["key_share"] = keyShare
		}
		ob := vless(tlsWith(fp, map[string]interface{}{"reality": r}))
		delete(ob["tls"].(map[string]interface{}), "insecure")
		ob["flow"] = "xtls-rprx-vision"
		return ob
	}
	cases["reality-chrome"] = reality("chrome", "")
	cases["reality-chrome_155"] = reality("chrome_155", "")
	cases["reality-chrome-classical"] = reality("chrome", "classical")
	// The fleet's XHTTP link: TLS, chrome, h2 and http/1.1 (hub links.go).
	xhttp := vless(tlsWith("chrome", map[string]interface{}{"alpn": []interface{}{"h2", "http/1.1"}}))
	xhttp["transport"] = map[string]interface{}{"type": "xhttp", "host": wireSNI, "path": "/x", "mode": "auto"}
	cases["xhttp-chrome"] = xhttp
	alpn := vless(tlsWith("chrome", map[string]interface{}{"alpn": []interface{}{"http/1.1"}}))
	cases["tls-chrome-alpn-http11"] = alpn
	return cases
}

// TestRecordWireHellos writes testdata/hello/*.bin from a real core: set
// HIKKARI_TEST_CORE to the binary and HIKKARI_WIRE_RECORD=1. The hellos carry
// throwaway keys and a made-up name, nothing of a real profile.
func TestRecordWireHellos(t *testing.T) {
	binary := os.Getenv("HIKKARI_TEST_CORE")
	if binary == "" || os.Getenv("HIKKARI_WIRE_RECORD") == "" {
		t.Skip("HIKKARI_TEST_CORE and HIKKARI_WIRE_RECORD are not set")
	}
	dir := filepath.Join("testdata", "hello")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, ob := range wireCases() {
		msg, records, err := mirrorHello(context.Background(), binary, ob)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		t.Logf("%s: %d bytes in %d records", name, len(msg), records)
		if err := os.WriteFile(filepath.Join(dir, name+".bin"), msg, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
