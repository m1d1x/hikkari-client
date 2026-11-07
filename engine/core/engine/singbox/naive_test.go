package singbox

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"NekkoVPN/core/engine"
	configfile "NekkoVPN/core/engine/singbox/config"
	"NekkoVPN/core/logging"
	"NekkoVPN/core/profile"
	"NekkoVPN/core/routing"
)

func TestNaiveProfileLifecycle(t *testing.T) {
	for _, scheme := range []string{"naive+https", "naive+quic"} {
		t.Run(scheme, func(t *testing.T) {
			uri := scheme + "://user%40test:p%3Ass%2F%3F%23@[::1]:8443?sni=proxy.example#%D0%A2%D0%B5%D1%81%D1%82%2520"
			d, err := ParseURI(uri)
			if err != nil {
				t.Fatal(err)
			}
			if d.Label != "Тест%20" || d.Config["username"] != "user@test" || d.Config["password"] != "p:ss/?#" || d.Config["server"] != "::1" || d.Config["quic"] != (scheme == "naive+quic") {
				t.Fatalf("wrong decoded profile: %#v", d)
			}
			b := &Backend{config: configfile.New(filepath.Join(t.TempDir(), "config.json")), tester: func() bool { return true }}
			decoded, err := b.Decode(uri)
			if err != nil {
				t.Fatal(err)
			}
			p := decoded[0]
			p.Name = "naive"
			if p.Connection == nil || p.Connection.Protocol != "naive" || p.Config != nil {
				t.Fatalf("not a portable profile: %#v", p)
			}
			// Go through on-disk JSON, where numbers and maps change types.
			raw, _ := json.Marshal(p)
			if err := json.Unmarshal(raw, &p); err != nil {
				t.Fatal(err)
			}
			out, err := b.Export(p)
			if err != nil {
				t.Fatal(err)
			}
			again, err := ParseURI(out)
			if err != nil || !reflect.DeepEqual(d, again) {
				t.Fatalf("round trip: %v, %#v", err, again)
			}
			if err := b.Configure(engine.Configuration{Profiles: []profile.Profile{p}, ActiveProfile: p.Name, Routing: routing.DefaultRoutingState()}); err != nil {
				t.Fatal(err)
			}
			cfg, err := b.config.Read()
			if err != nil {
				t.Fatal(err)
			}
			ob := taggedIn(cfg["outbounds"], "p-naive")
			if ob == nil || ob["type"] != "naive" || ob["password"] != "p:ss/?#" || ob["quic"] != (scheme == "naive+quic") {
				t.Fatalf("compiled outbound: %#v", ob)
			}
			_, retry := ob["flow_retry_delay"]
			if retry != (scheme == "naive+https") {
				t.Fatalf("wrong flow retry: %v", retry)
			}
			if binary := os.Getenv("HIKKARI_TEST_CORE"); binary != "" {
				if out, err := exec.Command(binary, "check", "-c", b.config.Path()).CombinedOutput(); err != nil {
					t.Fatalf("real core rejected Naive: %v\n%s", err, out)
				}
			}
		})
	}
}

func TestNaiveDefaultPortAndSubscription(t *testing.T) {
	uri := "naive+https://user:secret@proxy.example"
	d, err := ParseURI(uri)
	if err != nil || d.Config["server_port"] != 443 || d.Label != "proxy.example" {
		t.Fatalf("default: %#v %v", d, err)
	}
	for _, input := range []string{uri, "{\"outbounds\":[{\"type\":\"naive\",\"tag\":\"proxy.example\",\"server\":\"proxy.example\",\"server_port\":443,\"username\":\"user\",\"password\":\"secret\",\"tls\":{\"enabled\":true}}]}"} {
		got, err := ParseURIs(input)
		if err != nil || len(got) != 1 || !reflect.DeepEqual(got[0], d) {
			t.Fatalf("subscription: %#v %v", got, err)
		}
	}
}

func TestNaiveRejectsInvalidOrUnsupportedLinksWithoutSecrets(t *testing.T) {
	for _, uri := range []string{
		"naive+https://proxy.example", "naive+https://user@proxy.example", "naive+https://user:@proxy.example",
		"naive+https://user:SECRET@:443", "naive+https://user:SECRET@proxy.example:",
		"naive+https://user:SECRET@proxy.example:0", "naive+https://user:SECRET@proxy.example:65536",
		"naive+https://user:SECRET@proxy.example:bad", "naive+https://user%3Aname:SECRET@proxy.example", "naive+https://user:SECRET@proxy.example/path",
		"naive+https://user:SECRET@proxy.example?insecure=1", "naive+https://user:SECRET@proxy.example?alpn=h2",
		"naive+https://user:SECRET@proxy.example?sni=a&sni=b", "naive+https://user:SECRET@proxy.example?sni=%xx",
	} {
		_, err := ParseURI(uri)
		if err == nil || strings.Contains(err.Error(), "SECRET") {
			t.Fatalf("unsafe or missing error: %v", err)
		}
	}
	for _, scheme := range []string{"naive+https", "NAIVE+QUIC"} {
		got := logging.SanitizeLog("import " + scheme + "://user:SECRET@proxy.example:443#x")
		if got != "import [REDACTED_URI]" {
			t.Fatalf("secret in log: %s", got)
		}
	}
}

func TestNaiveExportRefusesSettingsItCannotPreserve(t *testing.T) {
	d, _ := ParseURI("naive+https://user:secret@proxy.example")
	for _, key := range []string{"insecure", "certificate", "utls", "alpn"} {
		d.Config["tls"] = map[string]interface{}{"enabled": true, key: true}
		if _, err := ExportURI(profile.Profile{Config: d.Config}); err == nil {
			t.Fatalf("lost %s", key)
		}
	}
	d.Config["tls"] = map[string]interface{}{"enabled": true}
	d.Config["extra_headers"] = map[string]interface{}{"X-Test": "secret"}
	if _, err := ExportURI(profile.Profile{Config: d.Config}); err == nil {
		t.Fatal("lost extra headers")
	}
}
