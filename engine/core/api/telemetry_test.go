package api

import (
	"encoding/json"
	"testing"
)

func TestTelemetrySwitchGovernsTheDowndetector(t *testing.T) {
	c := initCore(t)
	if !c.TelemetrySettings().Enabled || !c.DowndetectorSettings().Enabled {
		t.Fatalf("defaults: telemetry %+v, downdetector %+v", c.TelemetrySettings(), c.DowndetectorSettings())
	}
	if e := decode(t, c.Call("SetTelemetryEnabled", `{"enabled":false}`)); !e.Ok {
		t.Fatalf("SetTelemetryEnabled: %+v", e)
	}
	if c.DowndetectorSettings().Enabled {
		t.Fatal("downdetector still on after the switch went off")
	}
	// The downdetector's own setting cannot turn it back on behind the switch.
	_ = c.SetDowndetectorSettings(c.DowndetectorSettings())
	if e := decode(t, c.Call("SetDowndetectorSettings", `{"enabled":true,"share_private":true}`)); !e.Ok {
		t.Fatalf("SetDowndetectorSettings: %+v", e)
	}
	if s := c.DowndetectorSettings(); s.Enabled || !s.SharePrivate {
		t.Fatalf("downdetector = %+v", s)
	}
	_ = c.SetTelemetryEnabled(true)
	if !c.DowndetectorSettings().Enabled {
		t.Fatal("downdetector did not follow the switch back on")
	}
}

func TestTelemetryCallsKeepToTheContract(t *testing.T) {
	c := initCore(t)
	if e := decode(t, c.Call("TelemetryEvent", `{"name":"ui_open","fields":{"screen":"logs"}}`)); !e.Ok {
		t.Fatalf("valid event refused: %+v", e)
	}
	if e := decode(t, c.Call("TelemetryEvent", `{"name":"ui_open","fields":{"url":"https://example.org"}}`)); e.Ok {
		t.Fatal("an unlisted field was accepted")
	}
	if e := decode(t, c.Call("TelemetryError", `{"code":"ui.exception","component":"ui","message":"at 203.0.113.7"}`)); !e.Ok {
		t.Fatalf("valid error refused: %+v", e)
	}
	e := decode(t, c.Call("TelemetryLastBatch", ""))
	raw, _ := json.Marshal(e.Data)
	var b struct {
		Install string `json:"install"`
		Events  []struct {
			Name string `json:"name"`
		} `json:"events"`
		Errors []struct {
			Msg string `json:"msg"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatal(err)
	}
	if b.Install == "" || len(b.Errors) != 1 || b.Errors[0].Msg != "at <ip>" {
		t.Fatalf("batch = %s", raw)
	}
	names := map[string]bool{}
	for _, ev := range b.Events {
		names[ev.Name] = true
	}
	if !names["install"] || !names["launch"] || !names["ui_open"] {
		t.Fatalf("events = %v", names)
	}
}

func TestContractProtoFoldsEveryStack(t *testing.T) {
	cases := map[string]string{
		"vless-reality-tcp": "vless-reality", "vless-reality-xhttp": "vless-xhttp", "vless-ws": "other",
		"vmess-ws": "vmess", "trojan": "trojan", "hysteria2": "hysteria2", "shadowsocks": "shadowsocks", "": "other",
	}
	for slug, want := range cases {
		if got := contractProto(slug); got != want {
			t.Errorf("contractProto(%q) = %q, want %q", slug, got, want)
		}
	}
}
