package telemetry

import (
	"strings"
	"syscall"
)

// OSMajor is the macOS major version ("26"), nothing finer.
func OSMajor() string {
	v, err := syscall.Sysctl("kern.osproductversion")
	if err != nil {
		return ""
	}
	major, _, _ := strings.Cut(v, ".")
	return major
}
