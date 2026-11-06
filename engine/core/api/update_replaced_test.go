//go:build windows || darwin

package api

import "testing"

func TestReplacedUnder(t *testing.T) {
	for _, c := range []struct {
		own, installed string
		want           bool
	}{
		{"1.0.51", "1.0.40", true},
		{"1.0.51", "1.0.51", false},
		{"", "1.0.40", false},
		{"1.0.51", "", false},
	} {
		if got := replacedUnder(c.own, c.installed); got != c.want {
			t.Errorf("replacedUnder(%q, %q) = %v", c.own, c.installed, got)
		}
	}
}
