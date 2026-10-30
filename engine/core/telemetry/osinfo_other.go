//go:build !darwin && !windows

package telemetry

// OSMajor is unknown on platforms without a stable, coarse version source.
func OSMajor() string { return "" }
