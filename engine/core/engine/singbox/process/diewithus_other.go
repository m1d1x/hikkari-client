//go:build !windows

package vpn

// DieWithUs matters only where probes run: they need a socket that leaves past
// the TUN, which is Windows.
func DieWithUs(int) {}
