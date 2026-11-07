package api

import (
	"NekkoVPN/core/capacity"
	"NekkoVPN/core/engine"
	"NekkoVPN/core/uptime"
	"testing"
	"time"
)

func TestProfileGraphsUseProbersWhileTesterMeasurementsStayLocal(t *testing.T) {
	c := New()
	if err := c.InitWithEngine(manualRoutingDir(t), func(string, engine.Logger) (engine.Backend, error) {
		return &alternateEngine{caps: engine.Capabilities{TUN: true, AutoSelect: true}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	defer c.Shutdown()
	id, err := c.AddProfileFromURI("Prober profile")
	if err != nil {
		t.Fatal(err)
	}
	officialTestProfiles(t, c, id)
	pf, err := c.profiles.Read()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	pf.Subscriptions[0].Capacity = []capacity.Metric{{Key: capacity.Key(pf.Profiles[0].Connection.URI), Points: 1, MeasuredAt: now, ExpiresAt: now + 5400, RTTMS: 120, History: []capacity.Point{{Ts: now / 1800 * 1800, RTTMS: 120}}}}
	if err := c.profiles.Write(pf); err != nil {
		t.Fatal(err)
	}
	c.uptime.Record(id, true, 999, uptime.SrcTunnel)
	got := c.UptimeHistory()[id]
	if len(got) != 1 || got[0].RttMs != 120 || got[0].Src != uptime.SrcProber {
		t.Fatalf("local ping leaked into graph: %+v", got)
	}
	if len(c.uptimeHosts()) != 0 {
		t.Fatal("subscriber probes managed fleet")
	}
	if err := c.SetPresetTester(true); err != nil {
		t.Fatal(err)
	}
	if len(c.uptimeHosts()) != 1 {
		t.Fatal("tester diagnostics disabled")
	}
	if c.UptimeHistory()[id][0].RttMs != 120 || c.uptime.Snapshot()[id][0].RttMs != 999 {
		t.Fatal("public and tester histories mixed")
	}
}
