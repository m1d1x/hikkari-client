package api

import (
	"encoding/json"
	"testing"

	"NekkoVPN/core/downtime"
)

func TestTunnelFieldsAreThePeriodsNumbersWithoutZeros(t *testing.T) {
	got := tunnelFields(downtime.Period{
		From:          1_800_000_000,
		Minutes:       map[downtime.State]int{downtime.Flowing: 7, downtime.Idle: 0, downtime.Down: 2, downtime.NetDown: 1},
		StallMinutes:  9,
		StallBoundsMS: []int64{300, 1000, 3000},
		StallCount:    []int64{4, 1, 0},
		StallMS:       []int64{2600, 1400, 0},
	})
	raw, _ := json.Marshal(got)
	if want := `{"d":2,"n":1,"o":7,"s1000":1,"s1000_ms":1400,"s300":4,"s300_ms":2600,"stall_min":9}`; string(raw) != want {
		t.Fatalf("fields = %s\nwant     %s", raw, want)
	}
}

func TestTunnelFieldsLeaveOutStallsTheContractDoesNotName(t *testing.T) {
	got := tunnelFields(downtime.Period{
		Minutes:       map[downtime.State]int{downtime.Flowing: 3},
		StallMinutes:  3,
		StallBoundsMS: []int64{200, 1000, 3000},
		StallCount:    []int64{4, 1, 0},
		StallMS:       []int64{2600, 1400, 0},
	})
	raw, _ := json.Marshal(got)
	if string(raw) != `{"o":3}` {
		t.Fatalf("fields = %s", raw)
	}
	if f := tunnelFields(downtime.Period{Minutes: map[downtime.State]int{}}); f != nil {
		t.Fatalf("a period without a minute became an event: %v", f)
	}
}

func TestATunnelPeriodReachesTelemetryOnlyWhileTheSwitchIsOn(t *testing.T) {
	c := initCore(t)
	period := downtime.Period{From: 1_800_000_000, Minutes: map[downtime.State]int{downtime.Flowing: 10}}
	tunnels := func() int {
		n := 0
		if b := c.TelemetryLastBatch(); b != nil {
			for _, e := range b.Events {
				if e.Name == "tunnel" {
					n++
				}
			}
		}
		return n
	}
	c.reportTunnel(period)
	if tunnels() != 1 {
		t.Fatalf("queued %d tunnel events with the switch on", tunnels())
	}
	if err := c.SetTelemetryEnabled(false); err != nil {
		t.Fatal(err)
	}
	c.reportTunnel(period)
	if tunnels() != 0 {
		t.Fatalf("queued %d tunnel events with the switch off", tunnels())
	}
}
