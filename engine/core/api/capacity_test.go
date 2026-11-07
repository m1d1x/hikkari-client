package api

import (
	"NekkoVPN/core/capacity"
	"NekkoVPN/core/engine"
	"errors"
	"reflect"
	"testing"
	"time"
)

type advisingEngine struct {
	*alternateEngine
	advice []string
}

func (a *advisingEngine) SetAutoPreference(ids []string, _ int64) error {
	a.advice = append([]string(nil), ids...)
	return nil
}

func TestCapacityRefreshDoesNotReconfigureOrRestartVPN(t *testing.T) {
	backend := &advisingEngine{alternateEngine: &alternateEngine{caps: engine.Capabilities{TUN: true, AutoSelect: true}}}
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
	if err := c.SetAutoMode(true); err != nil {
		t.Fatal(err)
	}
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
	c.updateAutoPreference()
	if !reflect.DeepEqual(backend.advice, []string{id}) {
		t.Fatalf("advice was not applied separately: %v", backend.advice)
	}
	backend.configureErr = nil
}
