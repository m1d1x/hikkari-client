package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSubscriptionDeviceIDIsPersistent(t *testing.T) {
	dir := t.TempDir()
	c := &Core{baseDir: dir}
	id, err := c.subscriptionDeviceID()
	if err != nil {
		t.Fatal(err)
	}
	other := &Core{baseDir: dir}
	again, err := other.subscriptionDeviceID()
	if err != nil || id != again || len(id) != 32 {
		t.Fatal("installation ID changed across restart")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "subscription-device-id"))
	if err != nil || strings.TrimSpace(string(raw)) != id {
		t.Fatal("installation ID not persisted")
	}
	different, err := (&Core{baseDir: t.TempDir()}).subscriptionDeviceID()
	if err != nil || different == id {
		t.Fatal("different installations must get different IDs")
	}
}

func TestProviderRefusalLeavesProfilesUntouched(t *testing.T) {
	if vpnClashAPIRunningProbe() {
		t.Skip("real VPN is running")
	}
	c := initCore(t)
	if _, err := c.AddProfileFromURI("vless://id@127.0.0.1:443#Existing"); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Hwid") == "" {
			t.Error("API did not identify the installation")
		}
		w.Header().Set("X-Hwid-Max-Devices-Reached", "true")
		fmt.Fprint(w, "vless://id@127.0.0.1:443#Placeholder")
	}))
	defer server.Close()
	if _, err := c.AddProfileFromURI(server.URL); err == nil || !strings.Contains(err.Error(), "лимит устройств") {
		t.Fatalf("wrong provider error: %v", err)
	}
	profiles, err := c.Profiles()
	if err != nil || len(profiles) != 1 || profiles[0].Label != "Existing" {
		t.Fatal("provider refusal changed existing profiles")
	}
}

func TestDeleteLastProfileAndImportAgain(t *testing.T) {
	if vpnClashAPIRunningProbe() {
		t.Skip("real VPN is running")
	}
	c := initCore(t)
	name, err := c.AddProfileFromURI("vless://id@127.0.0.1:443#Only")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SwitchProfile(name); err != nil {
		t.Fatal(err)
	}
	if err := c.SetAutoMode(true); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteProfile(name); err != nil {
		t.Fatal(err)
	}
	pf, err := c.profiles.Read()
	if err != nil || len(pf.Profiles) != 0 || pf.Active != "" || pf.Auto {
		t.Fatal("last profile state was not cleared")
	}
	cfg, err := readBuiltConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	if cfg["route"].(map[string]interface{})["final"] != "direct" {
		t.Fatal("empty config references missing selector")
	}
	for _, start := range []func() error{c.StartVPN, c.RestartVPN} {
		if err := start(); err == nil || !strings.Contains(err.Error(), "добавьте сервер") {
			t.Fatalf("empty VPN must not start: %v", err)
		}
	}
	if _, err := c.AddProfileFromURI("vless://id@127.0.0.1:443#New"); err != nil {
		t.Fatal(err)
	}
	profiles, err := c.Profiles()
	if err != nil || len(profiles) != 1 || profiles[0].Label != "New" {
		t.Fatal("cannot import after deleting the last profile")
	}
}

// Provider envelopes and identity behave identically for a transport that
// has no knowledge of sing-box or HTTP subscriptions.
func TestSubscriptionWithAlternateEngine(t *testing.T) {
	c := initAlternate(t, &alternateEngine{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Hwid") == "" {
			t.Error("missing shared installation identity")
		}
		fmt.Fprint(w, "alternate://one#One\nalternate://two#Two")
	}))
	defer server.Close()
	if _, err := c.AddProfileFromURI(server.URL); err != nil {
		t.Fatal(err)
	}
	profiles, _ := c.Profiles()
	if len(profiles) != 2 || profiles[0].Protocol != "ALTERNATE" {
		t.Fatal("provider depended on concrete transport")
	}
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "alternate://placeholder#Unsupported%20client")
	}))
	defer blocked.Close()
	if _, err := c.AddProfileFromURI(blocked.URL); err == nil {
		t.Fatal("provider placeholder reached alternate decoder")
	}
	after, _ := c.Profiles()
	if len(after) != 2 {
		t.Fatal("refusal changed profiles")
	}
}

func TestLegacyInstallSurvivesTwoRestarts(t *testing.T) {
	if vpnClashAPIRunningProbe() {
		t.Skip("real VPN is running")
	}
	dir := t.TempDir()
	files := map[string]string{
		"profiles.json":               `{"active":"p-server","profiles":[{"name":"server","label":"Server","kind":"outbound","config":{"type":"vless","server":"127.0.0.1","server_port":443,"uuid":"id","custom_legacy_option":"preserve"}},{"name":"p-server","label":"Other","kind":"outbound","config":{"type":"vless","server":"127.0.0.1","server_port":8443,"uuid":"id2"}}]}`,
		"routing.json":                `{"enabled":true,"mode":"bypass","bypass":{"domains":["example.test"],"ips":["192.0.2.0/24"],"processes":["Browser"]},"proxy":{"domains":[],"ips":[],"processes":[]}}`,
		"service-uptime-history.json": `{"p-server|Test":[{"ts":1,"ok":true,"rtt_ms":42}],"p-p-server|Test":[{"ts":2,"ok":true,"rtt_ms":84}]}`,
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		c := New()
		if err := c.Init(dir); err != nil {
			t.Fatal(err)
		}
		pf, err := c.profiles.Read()
		if err != nil || len(pf.Profiles) != 2 || pf.Active != "p-server" || pf.Profiles[0].Connection == nil || pf.Profiles[0].Config["custom_legacy_option"] != "preserve" {
			t.Fatalf("profiles changed on restart %d: %+v %v", i, pf, err)
		}
		policy, err := c.Routing()
		if err != nil || len(policy.Bypass.Processes) != 1 || len(policy.Bypass.Domains) != 1 || len(policy.Bypass.IPs) != 1 {
			t.Fatal("routing lost")
		}
		history := c.ServiceUptimeHistory()
		if len(history) != 2 || len(history["server|Test"]) != 1 || history["server|Test"][0].RttMs != 42 || len(history["p-server|Test"]) != 1 || history["p-server|Test"][0].RttMs != 84 {
			t.Fatalf("history corrupted on restart %d: %+v", i, history)
		}
		if err := c.Shutdown(); err != nil {
			t.Fatal(err)
		}
	}
}
