//go:build !windows && !darwin

package api

import "NekkoVPN/core/svcproto"

// There is no service to run the deep reset on other platforms.
func (c *Core) ResetNetworkStack(force bool) (svcproto.NetworkReset, error) {
	return svcproto.NetworkReset{}, errUnsupportedNetworkReset
}

func (c *Core) ScanNetworkStack() (svcproto.NetworkScan, error) {
	return svcproto.NetworkScan{}, errUnsupportedNetworkReset
}
