//go:build darwin

package api

import (
	"time"

	"NekkoVPN/core/netdoctor"
	"NekkoVPN/core/svcclient"
	"NekkoVPN/core/svcproto"
)

// The settings the doctor repairs belong to the whole machine on macOS and
// take root to change, so the daemon does it. Reading takes no rights, which
// keeps the check before every connect out of the daemon until there is
// something to fix.

const networkRepairTimeout = 30 * time.Second

func repairHost() (netdoctor.RepairResult, error) {
	before, err := netdoctor.Diagnose()
	if err != nil {
		return netdoctor.RepairResult{}, err
	}
	actions, err := callRepair(false)
	if err != nil {
		return netdoctor.RepairResult{}, err
	}
	return netdoctor.RepairResult{Before: before, Actions: actions}, nil
}

func sanitizeHostProxy() (netdoctor.Action, error) {
	a := netdoctor.Action{Name: "proxy_disable"}
	rep, err := netdoctor.Diagnose()
	if err != nil {
		return a, err
	}
	if rep.Proxy.Verdict != netdoctor.ProxyOrphan {
		return a, nil
	}
	actions, err := callRepair(true)
	if err != nil {
		return a, err
	}
	if len(actions) > 0 {
		a = actions[0]
	}
	return a, nil
}

func callRepair(proxyOnly bool) ([]netdoctor.Action, error) {
	resp, err := svcclient.Call(svcproto.Request{Op: svcproto.OpRepairNetwork, ProxyOnly: proxyOnly}, networkRepairTimeout)
	if err != nil {
		return nil, err
	}
	if resp.NetworkRepair == nil {
		return nil, errUnsupportedNetworkReset
	}
	actions := make([]netdoctor.Action, 0, len(resp.NetworkRepair.Actions))
	for _, a := range resp.NetworkRepair.Actions {
		actions = append(actions, netdoctor.Action{Name: a.Name, Done: a.Done, Detail: a.Detail})
	}
	return actions, nil
}
