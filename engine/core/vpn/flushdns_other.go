//go:build !windows

package vpn

// FlushDNS is a no-op outside Windows; see the Windows implementation.
func FlushDNS() {}
