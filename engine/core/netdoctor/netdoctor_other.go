//go:build !windows && !darwin

package netdoctor

import "context"

// The settings the doctor repairs are specific to Windows and macOS; elsewhere it reports
// that it has nothing to check and changes nothing.

func Diagnose() (Report, error) {
	return Report{Supported: false, Proxy: ProxyReport{Verdict: ProxyNone}}, nil
}

func Repair() (RepairResult, error) {
	before, _ := Diagnose()
	return RepairResult{Before: before}, nil
}

func SanitizeProxy() (Action, error) {
	return Action{Name: "proxy_disable", Detail: "no system proxy"}, nil
}

// Host selection probes failure modes of those systems; elsewhere the core
// keeps its defaults.

func SelectHost() Selection {
	return Selection{Iface: IfaceUnchecked, DNS: DNSUnchecked, Probed: true}
}

func CurrentNetwork() string { return "" }

func WatchNetwork(ctx context.Context, _ func(string)) error {
	<-ctx.Done()
	return nil
}
