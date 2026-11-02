package api

import (
	"strings"
	"testing"
)

func TestDoctorLinesAreOneFieldEach(t *testing.T) {
	report := NetworkReport{HostsOverrides: 3, DPITools: []string{"goodbyedpi"}, ScanError: "no service"}
	lines := doctorLines(report)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"hostsOverrides=3", `dpiTools=["goodbyedpi"]`, `scanError="no service"`} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in:\n%s", want, joined)
		}
	}
	for i := 1; i < len(lines); i++ {
		if lines[i-1] > lines[i] {
			t.Fatalf("not sorted: %q > %q", lines[i-1], lines[i])
		}
	}
}
