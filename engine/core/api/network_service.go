//go:build windows || darwin

package api

import (
	"time"

	"NekkoVPN/core/svcclient"
	"NekkoVPN/core/svcproto"
)

// networkResetTimeout: netsh rebuilds the Winsock catalog and TCP/IP keys,
// which takes several seconds on a slow disk, after up to 30 s of stopping
// DPI tools.
const networkResetTimeout = 120 * time.Second

// ScanNetworkStack asks the service, read-only, what the deep reset would
// deal with (hosts overrides of protected domains, DPI tools) and which
// foreign WFP providers are present.
func (c *Core) ScanNetworkStack() (svcproto.NetworkScan, error) {
	status, err := svcclient.Call(svcproto.Request{Op: svcproto.OpStatus}, 5*time.Second)
	if err != nil {
		return svcproto.NetworkScan{}, err
	}
	if !status.ScanSafe {
		return svcproto.NetworkScan{}, errServiceUpdating
	}
	resp, err := svcclient.Call(svcproto.Request{Op: svcproto.OpScanNetwork}, 10*time.Second)
	if err != nil {
		return svcproto.NetworkScan{}, err
	}
	if resp.NetworkScan == nil {
		return svcproto.NetworkScan{}, errUnsupportedNetworkReset
	}
	scan := *resp.NetworkScan
	if c.logger != nil {
		c.logger.Logf("[netdoctor] scan hosts_overrides=%d dpi=%s foreign_wfp=%s", scan.HostsOverrides, svcproto.ScanList(scan.DPITools), svcproto.ScanList(scan.ForeignWFP))
	}
	return scan, nil
}

// ResetNetworkStack asks the service for the deep reset (Winsock, TCP/IP).
// The tunnel is stopped first through the GUI's own path, so the engine's
// state follows. Without force the service refuses while adapters with a
// static address would lose it, and reports them for the UI to ask about.
func (c *Core) ResetNetworkStack(force bool) (svcproto.NetworkReset, error) {
	if err := c.StopVPN(); err != nil {
		return svcproto.NetworkReset{}, err
	}
	resp, err := svcclient.Call(svcproto.Request{Op: svcproto.OpResetNetwork, Force: force}, networkResetTimeout)
	if err != nil {
		return svcproto.NetworkReset{}, err
	}
	if resp.NetworkReset == nil {
		return svcproto.NetworkReset{}, errUnsupportedNetworkReset
	}
	res := *resp.NetworkReset
	if c.logger != nil && !res.Refused {
		c.logger.Logf("[netdoctor] deep reset hosts_disabled=%d dpi_stopped=%v dpi_failed=%v", res.HostsDisabled, res.DPIStopped, res.DPIFailed)
	}
	return res, nil
}
