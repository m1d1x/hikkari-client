package api

import (
	"context"
	"runtime"

	"NekkoVPN/core/netdoctor"
)

// Host network selection (core/netdoctor): which interface and resolver the
// core's direct path uses on this machine. Probed with the tunnel down, at
// connect; kept for the physical network it was made on, so routing changes
// and reconnects on the same network do not re-probe and cannot flip it.

// selectHostLocked refreshes c.host before a connect. A selection made on
// the current network is reused. Callers hold operationMu, tunnel down.
func (c *Core) selectHostLocked() {
	if c.host.Probed && c.host.Network != "" && c.host.Network == netdoctor.CurrentNetwork() {
		return
	}
	c.host = netdoctor.SelectHost()
	if c.logger != nil {
		c.logger.Logf("[netdoctor] %s", c.host.LogLine())
	}
}

// forgetHostLocked drops the cached selection; the next connect probes
// afresh. «починить сеть» uses it. Callers hold operationMu.
func (c *Core) forgetHostLocked() { c.host = netdoctor.Selection{} }

// watchNetwork re-evaluates the selection when the physical network
// changes under a running tunnel. A tunnel on defaults needs nothing: the
// core's auto-detect follows the change itself. A pinned interface may be
// gone and a fallback resolver may no longer be needed, so such a tunnel is
// restarted, which probes again with the tunnel down.
func (c *Core) watchNetwork(ctx context.Context) {
	err := netdoctor.WatchNetwork(ctx, func(string) {
		c.health.RecheckEgress()
		c.operationMu.Lock()
		defer c.operationMu.Unlock()
		if ctx.Err() != nil || !c.backend.IsRunning() || c.host.Default() {
			return
		}
		if c.logger != nil {
			c.logger.Logf("[netdoctor] network changed with %s, reconnecting", c.host.LogLine())
		}
		c.forgetHostLocked()
		if err := c.restartVPN(); err != nil && c.logger != nil {
			c.logger.Logf("[netdoctor] reconnect after network change failed: %v", err)
		}
	})
	if err != nil && c.logger != nil {
		c.logger.Logf("[netdoctor] network watch unavailable: %v", err)
	}
}

// NetworkReport is what «проверить» shows: the system settings the doctor
// repairs, and the direct-path selection.
type NetworkReport struct {
	netdoctor.Report
	Host netdoctor.Selection `json:"host"`
	// HostsOverrides counts hosts entries overriding protected domains; the
	// deep reset disables them. -1 when the scan could not run (no service).
	HostsOverrides int `json:"hostsOverrides"`
	// DPITools and ForeignWFP: see svcproto.NetworkScan. Null when unknown,
	// also when the scan could not run.
	DPITools   []string `json:"dpiTools"`
	ForeignWFP []string `json:"foreignWfp"`
}

// NetworkDiagnose inspects the machine without changing it. With the
// tunnel up the direct path cannot be probed (strict_route sends the probes
// into the TUN), so the selection made at connect is reported instead.
func (c *Core) NetworkDiagnose() (NetworkReport, error) {
	rep, err := netdoctor.Diagnose()
	if err != nil {
		return NetworkReport{}, err
	}
	rep.OS = runtime.GOOS
	report := NetworkReport{Report: rep, HostsOverrides: -1}
	if scan, err := c.ScanNetworkStack(); err == nil {
		report.HostsOverrides, report.DPITools, report.ForeignWFP = scan.HostsOverrides, scan.DPITools, scan.ForeignWFP
	}
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	if c.backend.IsRunning() {
		report.Host = c.host
		report.Host.Probed = false
		return report, nil
	}
	c.host = netdoctor.SelectHost()
	report.Host = c.host
	return report, nil
}

// NetworkRepair is «починить сеть»: the soft repair, then a fresh direct-
// path selection — through a reconnect when the tunnel is up.
func (c *Core) NetworkRepair() (netdoctor.RepairResult, error) {
	res, err := repairHost()
	if err != nil {
		return res, err
	}
	if c.logger != nil {
		for _, a := range res.Actions {
			// Details name the proxy endpoint; the log keeps only the outcome.
			c.logger.Logf("[netdoctor] repair %s done=%v", a.Name, a.Done)
		}
	}
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	c.forgetHostLocked()
	if c.backend.IsRunning() {
		reconnect := netdoctor.Action{Name: "reconnect"}
		if err := c.restartVPN(); err != nil {
			reconnect.Detail = err.Error()
		} else {
			reconnect.Done = true
		}
		res.Actions = append(res.Actions, reconnect)
	}
	return res, nil
}
