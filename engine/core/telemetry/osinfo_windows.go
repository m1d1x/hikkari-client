package telemetry

import (
	"strconv"

	"golang.org/x/sys/windows"
)

// OSMajor is "10" or "11" — Windows 11 still reports major 10, so the
// build number tells them apart. Nothing finer leaves the device.
func OSMajor() string {
	v := windows.RtlGetVersion()
	if v.MajorVersion == 10 && v.BuildNumber >= 22000 {
		return "11"
	}
	return strconv.FormatUint(uint64(v.MajorVersion), 10)
}
