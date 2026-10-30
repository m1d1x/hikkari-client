package routing

// Target describes user intent; native outbound tags belong to adapters.
type Target string

const (
	TargetDirect Target = "direct"
	TargetVPN    Target = "vpn"
)

// Policy is the common execution plan. Category matchers are alternatives
// (domain OR subnet OR process), not an intersection. Domains match the named
// host and its subdomains, IPs are CIDRs, processes are exact executable names, folders are path fragments.
// System DNS/TUN rules are supplied by each adapter independently.
type Policy struct {
	Rules         RouteList `json:"rules"`
	Target        Target    `json:"target"`
	DefaultTarget Target    `json:"defaultTarget"`
}

func (s RoutingState) Policy() Policy {
	p := Policy{Rules: RouteList{Domains: []string{}, IPs: []string{}, Processes: []string{}, Folders: []string{}}, Target: TargetDirect, DefaultTarget: TargetVPN}
	if !s.Enabled {
		return p
	}
	if s.Mode == ModeProxy {
		p.Rules = s.Proxy
		p.Target, p.DefaultTarget = TargetVPN, TargetDirect
	} else {
		p.Rules = s.Bypass
	}
	return p
}

// ActiveEqual compares only behavior that can affect the current connection.
// Source changes, inactive-list edits and ordering of duplicate rules do not
// require the UI to reason about restarting native engines.
func ActiveEqual(a, b RoutingState) bool {
	ap, bp := a.Policy(), b.Policy()
	if ap.DefaultTarget != bp.DefaultTarget {
		return false
	}
	empty := func(r RouteList) bool { return len(r.Domains)+len(r.IPs)+len(r.Processes)+len(r.Folders) == 0 }
	if empty(ap.Rules) && empty(bp.Rules) {
		return true
	}
	if ap.Target != bp.Target {
		return false
	}
	same := func(a, b []string) bool {
		set := map[string]bool{}
		for _, value := range a {
			set[value] = true
		}
		for _, value := range b {
			if !set[value] {
				return false
			}
		}
		for _, value := range b {
			delete(set, value)
		}
		return len(set) == 0
	}
	return same(ap.Rules.Domains, bp.Rules.Domains) && same(ap.Rules.IPs, bp.Rules.IPs) && same(ap.Rules.Processes, bp.Rules.Processes) && same(ap.Rules.Folders, bp.Rules.Folders)
}
