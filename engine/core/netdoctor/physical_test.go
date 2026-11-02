//go:build windows || darwin

package netdoctor

import (
	"context"
	"net"
	"testing"
	"time"
)

type fakePhysical struct {
	cs    []Candidate
	dials []int
}

func (f *fakePhysical) candidates() ([]Candidate, error) { return f.cs, nil }

func (f *fakePhysical) dialer(idx int, timeout time.Duration) *net.Dialer {
	f.dials = append(f.dials, idx)
	return &net.Dialer{Timeout: timeout}
}

func TestPhysicalCandidatePrefersBestPhysicalMetric(t *testing.T) {
	env := &fakePhysical{cs: []Candidate{
		{Index: 1, Metric: 5, Physical: false},
		{Index: 2, Metric: 50, Physical: true},
		{Index: 3, Metric: 20, Physical: true},
	}}
	c, err := physicalCandidate(env)
	if err != nil || c.Index != 3 {
		t.Fatalf("got %+v, %v; want interface 3", c, err)
	}
}

func TestPhysicalCandidateFailsWithoutPhysicalInterface(t *testing.T) {
	env := &fakePhysical{cs: []Candidate{{Index: 1, Metric: 5}}}
	if _, err := physicalCandidate(env); err == nil {
		t.Fatal("virtual-only machine must not yield a physical interface")
	}
}

func TestPhysicalDialBindsChosenInterface(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		if c, err := ln.Accept(); err == nil {
			c.Close()
		}
	}()
	env := &fakePhysical{cs: []Candidate{{Index: 7, Metric: 1, Physical: true}}}
	conn, err := physicalDial(env)(context.Background(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if len(env.dials) != 1 || env.dials[0] != 7 {
		t.Fatalf("dialed through %v, want [7]", env.dials)
	}
}
