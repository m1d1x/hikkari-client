package api

import (
	diag "NekkoVPN/core/engine/singbox/diagnostics"
	vpn "NekkoVPN/core/engine/singbox/process"
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"NekkoVPN/core/engine"
	"NekkoVPN/core/netdoctor"
	"NekkoVPN/core/svcproto"
	"NekkoVPN/core/testerlog"
)

// Host network selection (core/netdoctor): which interface and resolver the
// core's direct path uses on this machine. Probed with the tunnel down, at
// connect; kept for the physical network it was made on, so routing changes
// and reconnects on the same network do not re-probe and cannot flip it.

// selectHostLocked refreshes c.host before a connect. A selection made on
// the current network is reused. Callers hold operationMu, tunnel down.
func (c *Core) selectHostLocked() {
	if c.host.Probed && c.host.Network != "" && c.host.Network == netdoctor.CurrentNetwork() {
		if c.logger != nil {
			c.logger.Logf("[netdoctor] selection reused: %s %s", c.host.LogLine(), c.host.PinLine())
		}
		return
	}
	c.host = netdoctor.SelectHost()
	if c.logger != nil {
		c.logger.Logf("[netdoctor] %s %s", c.host.LogLine(), c.host.PinLine())
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
		if c.autoTunnel != nil {
			if err := c.autoTunnel.Reset(); err != nil && c.logger != nil {
				c.logger.Logf("[autotunnel] reset after network change: %v", err)
			}
		}
		c.operationMu.Lock()
		defer c.operationMu.Unlock()
		// Unprobed (a resumed core) is not known to be on defaults where the
		// resolver is pinned.
		if ctx.Err() != nil || !c.backend.IsRunning() || (c.host.Default() && !netdoctor.PinsSystemResolver) {
			return
		}
		if c.logger != nil {
			c.logger.Logf("[netdoctor] network changed with %s, reconnecting", c.host.LogLine())
		}
		c.forgetHostLocked()
		if err := c.restartVPN("physical-network-change"); err != nil && c.logger != nil {
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
	// WinDivert: see svcproto.NetworkScan.
	WinDivert *svcproto.WinDivertState `json:"winDivert,omitempty"`
	// SecureDNS is the Secure DNS setting of each browser on this machine.
	SecureDNS []netdoctor.BrowserSecureDNS `json:"secureDns"`
	// ScanError says why the scan could not run; a check that did not happen
	// must not read as a clean machine.
	ScanError string `json:"scanError,omitempty"`
	// Direct is what the direct path reaches right now.
	Direct netdoctor.DirectPath `json:"direct"`
}

// NetworkDiagnose inspects the machine without changing it. With the
// tunnel up the direct path cannot be probed (strict_route sends the probes
// into the TUN), so the selection made at connect is reported instead.
func (c *Core) NetworkDiagnose() (NetworkReport, error) {
	report, err := c.networkDiagnose()
	if err != nil {
		return report, err
	}
	// A tester's check lands in the stream on its own: nobody has to ask for it.
	if c.logger != nil && c.presets.Snapshot().Tester {
		for _, line := range doctorLines(report) {
			c.logger.Logf("[doctor] %s", line)
		}
	}
	return report, nil
}

func (c *Core) networkDiagnose() (NetworkReport, error) {
	rep, err := netdoctor.Diagnose()
	if err != nil {
		return NetworkReport{}, err
	}
	rep.OS = runtime.GOOS
	report := NetworkReport{Report: rep, HostsOverrides: -1}
	scan, err := c.ScanNetworkStack()
	switch {
	case err == nil:
		report.HostsOverrides, report.DPITools, report.ForeignWFP = scan.HostsOverrides, scan.DPITools, scan.ForeignWFP
		report.WinDivert = scan.WinDivert
	case !errors.Is(err, errUnsupportedNetworkReset):
		report.ScanError = err.Error()
		if c.logger != nil {
			c.logger.Logf("[netdoctor] scan failed: %v", err)
		}
	}
	running := c.diagnoseHost(&report)
	report.Direct = c.probeDirectPath(running)
	report.SecureDNS = netdoctor.SecureDNSSettings()
	if c.logger != nil {
		c.logger.Logf("[netdoctor] %s direct_checked=%v internet=%v secure_dns_blocked=%v browsers=%s tunnel=%v",
			report.Host.LogLine(), report.Direct.Checked, report.Direct.Internet, report.Direct.SecureDNSBlocked, secureDNSLog(report.SecureDNS), running)
	}
	return report, nil
}

// doctorLines renders a report for the tester stream, one key=json per field:
// a line stays under the stream's limit, and an LLM reads a field at a time.
func doctorLines(report NetworkReport) []string {
	data, err := json.Marshal(report)
	if err != nil {
		return []string{"error=" + strconv.Quote(err.Error())}
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return []string{"error=" + strconv.Quote(err.Error())}
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, k+"="+string(fields[k]))
	}
	return lines
}

// testerCommands is what the hub may ask of this install (engine/core/testerlog).
// All only read the machine: run_doctor never repairs, a snapshot writes its
// [diag:*] lines into the log as the session diagnostics do, load_profiles runs
// each profile in a core of its own beside the tunnel as the periodic round
// does, and probe_targets opens and closes bare TCP connections to the
// addresses the operator named.
func (c *Core) testerCommands() map[string]testerlog.Command {
	return map[string]testerlog.Command{
		"run_doctor": {Source: "doctor", Run: func(context.Context, json.RawMessage) []string {
			report, err := c.networkDiagnose()
			if err != nil {
				return []string{"error=" + strconv.Quote(err.Error())}
			}
			return doctorLines(report)
		}},
		"snapshot": {Source: "snapshot", Run: func(context.Context, json.RawMessage) []string {
			snapshotter, ok := c.backend.(interface{ Snapshot() })
			if !ok {
				return []string{"error=\"this engine has no snapshot\""}
			}
			snapshotter.Snapshot()
			return nil
		}},
		"load_profiles": {Source: "diag:load", Run: func(context.Context, json.RawMessage) []string {
			loader, ok := c.backend.(interface{ LoadNow() []string })
			if !ok {
				return []string{"error=\"this engine has no load round\""}
			}
			return loader.LoadNow()
		}},
		"probe_targets": {Source: "probe", Run: diag.ProbeTargets},
		"proto_targets": {Source: "protoprobe", Run: func(ctx context.Context, params json.RawMessage) []string {
			return diag.ProtoTargets(ctx, vpn.CoreBinary(), params)
		}},
	}
}

// maxReportLines bounds what one report adds to the tester stream.
const maxReportLines = 200

// logReportForTester puts a tester's report into the stream too: tickets and the
// stream are read in different places, and the stream is the one that keeps the
// lines around it.
func (c *Core) logReportForTester(id, message string) {
	if c.logger == nil || !c.presets.Snapshot().Tester {
		return
	}
	lines := strings.Split(message, "\n")
	if len(lines) > maxReportLines {
		lines = lines[:maxReportLines]
	}
	for _, line := range lines {
		c.logger.Logf("[support] report %s: %s", id, line)
	}
}

// secureDNSLog renders the browsers' settings for a log line.
func secureDNSLog(browsers []netdoctor.BrowserSecureDNS) string {
	parts := make([]string, 0, len(browsers))
	for _, b := range browsers {
		parts = append(parts, b.Browser+":"+b.Mode+":"+b.Resolver)
	}
	return "[" + strings.Join(parts, " ") + "]"
}

// tunnelDownSettle lets the routes of a stopped tunnel go away before measuring.
const tunnelDownSettle = 3 * time.Second

// measureTunnelDown is what the network does with the tunnel down, for the
// tester stream: it answers «without the VPN nothing works» on its own, without
// asking the tester to run anything. Only testers, once per stop and per start
// with the tunnel down, never periodically. A machine with no network keeps the
// line in the stream's buffer until it has one.
func (c *Core) measureTunnelDown() {
	if !c.presets.Snapshot().Tester || c.logger == nil {
		return
	}
	select {
	case <-c.ctx.Done():
		return
	case <-time.After(tunnelDownSettle):
	}
	if c.backend.IsRunning() {
		return // a reconnect: the requests would go through the tunnel
	}
	host := netdoctor.SelectHost()
	direct := netdoctor.ProbeDirectPath(netdoctor.FetchDirect)
	if c.backend.IsRunning() {
		return
	}
	c.logger.Logf("[netdoctor] tunnel down: %s internet=%v secure_dns_blocked=%v browsers=%s",
		host.LogLine(), direct.Internet, direct.SecureDNSBlocked, secureDNSLog(netdoctor.SecureDNSSettings()))
}

// diagnoseHost fills in the selection and reports whether the tunnel is up.
func (c *Core) diagnoseHost(report *NetworkReport) bool {
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	if c.backend.IsRunning() {
		report.Host = c.host
		report.Host.Probed = false
		return true
	}
	c.host = netdoctor.SelectHost()
	report.Host = c.host
	return false
}

// probeDirectPath measures the direct path. It runs outside operationMu: the
// requests take seconds, and a connect must not wait behind a check.
func (c *Core) probeDirectPath(running bool) netdoctor.DirectPath {
	if !running {
		return netdoctor.ProbeDirectPath(netdoctor.FetchDirect)
	}
	prober, ok := c.backend.(engine.DirectProber)
	if !ok || !prober.CanProbeDirect() {
		return netdoctor.DirectPath{}
	}
	return netdoctor.ProbeDirectPath(func(url string) error {
		return prober.ProbeDirect(url, int(netdoctor.DirectProbeTimeout/time.Millisecond))
	})
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
		if err := c.restartVPN("network-repair"); err != nil {
			reconnect.Detail = err.Error()
		} else {
			reconnect.Done = true
		}
		res.Actions = append(res.Actions, reconnect)
	}
	return res, nil
}
