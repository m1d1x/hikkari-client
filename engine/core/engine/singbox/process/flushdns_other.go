//go:build !windows && !darwin

package vpn

// FlushDNS is a no-op where no service runs the core; see the Windows and
// macOS implementations.
func FlushDNS() {}
