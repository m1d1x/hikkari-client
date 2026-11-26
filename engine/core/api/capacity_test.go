package api

import (
	"NekkoVPN/core/capacity"
	"NekkoVPN/core/engine"
	"errors"
	"testing"
	"time"
)

func TestCapacityRefreshDoesNotReconfigureOrRestartVPN(t *testing.T) {
	backend := &alternateEngine{caps: engine.Capabilities{TUN: true}}
	c := New()
	if err := c.InitWithEngine(manualRoutingDir(t), func(string, engine.Logger) (engine.Backend, error) { return backend, nil }); err != nil {
		t.Fatal(err)
	}
	defer c.Shutdown()
	id, err := c.AddProfileFromURI("Our relay")
	if err != nil {
		t.Fatal(err)
	}
	officialTestProfiles(t, c, id)
	before, err := c.profiles.Read()
	if err != nil {
		t.Fatal(err)
	}
	after, err := cloneProfiles(before)
	if err != nil {
		t.Fatal(err)
	}
	after.Subscriptions[0].Capacity = []capacity.Metric{{Key: capacity.Key(after.Profiles[0].Connection.URI), Mbps: 100, Points: 1, MeasuredAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Hour).Unix()}}
	backend.configureErr = errors.New("metadata must not reach Configure")
	if err := c.commitProfiles(c.subscriptionContext(), before, after); err != nil {
		t.Fatalf("capacity refresh touched runtime configuration: %v", err)
	}
	backend.configureErr = nil
}
