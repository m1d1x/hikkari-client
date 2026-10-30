package xray

import (
	"NekkoVPN/core/engine"
	"NekkoVPN/core/routing"
	"fmt"
)

const tunTag = "hikkari-tun"

// CompileVPN translates client intent directly into Xray TUN, DNS and routing.
// No other adapter or native configuration participates in this conversion.
func CompileVPN(c engine.Configuration, ports map[string]int) (object, error) {
	cfg, err := Compile(c.Profiles, ports)
	if err != nil {
		return nil, err
	}
	inbounds := cfg["inbounds"].([]interface{})
	cfg["inbounds"] = append(inbounds, object{
		"tag": tunTag, "protocol": "tun", "settings": object{
			"mtu": 1360, "gateway": []string{"172.31.7.1/30"},
			"autoSystemRoutingTable": []string{"0.0.0.0/0", "::/0"}, "autoOutboundsInterface": "auto",
		}, "sniffing": object{"enabled": true, "destOverride": []string{"http", "tls", "quic"}, "routeOnly": true},
	})
	outs := cfg["outbounds"].([]interface{})
	outs = append(outs, object{"tag": "direct", "protocol": "freedom", "settings": object{"finalRules": []interface{}{object{"action": "allow"}}}})
	outs = append(outs, object{"tag": "dns-out", "protocol": "dns"})
	cfg["outbounds"] = outs
	// DNS forwarding is routed through the selected server; it never implicitly
	// follows the direct outbound. The process host directs system DNS at TUN.
	cfg["dns"] = object{"tag": "dns-query", "servers": []string{"https://1.1.1.1/dns-query"}, "queryStrategy": "UseIPv4"}
	nativeRoute := cfg["routing"].(object)
	rules := nativeRoute["rules"].([]interface{}) // per-profile probes stay isolated
	selected := "reject"
	for i, p := range c.Profiles {
		if p.Name == c.ActiveProfile {
			selected = fmt.Sprintf("transport-%d", i)
		}
	}
	if selected == "reject" && len(c.Profiles) > 0 {
		selected = "transport-0"
	}
	destination := object{"outboundTag": selected}
	if c.AutoSelect && len(c.Profiles) > 0 {
		cfg["observatory"] = object{"subjectSelector": []string{"transport-"}, "probeURL": "https://cp.cloudflare.com/generate_204", "probeInterval": "30s", "enableConcurrency": true}
		nativeRoute["balancers"] = []interface{}{object{"tag": "auto", "selector": []string{"transport-"}, "strategy": object{"type": "leastPing"}, "fallbackTag": "reject"}}
		destination = object{"balancerTag": "auto"}
	}
	add := func(match object, target object) {
		rule := object{"type": "field"}
		for k, v := range match {
			rule[k] = v
		}
		for k, v := range target {
			rule[k] = v
		}
		rules = append(rules, rule)
	}
	add(object{"inboundTag": []string{tunTag}, "port": "53"}, object{"outboundTag": "dns-out"})
	add(object{"inboundTag": []string{"dns-query"}}, destination)
	policy := c.Routing.Policy()
	list := policy.Rules
	final := destination
	if policy.DefaultTarget == routing.TargetDirect {
		final = object{"outboundTag": "direct"}
	}
	target := destination
	if policy.Target == routing.TargetDirect {
		target = object{"outboundTag": "direct"}
	}

	if len(list.Domains) > 0 {
		domains := make([]string, 0, len(list.Domains))
		for _, domain := range list.Domains {
			domains = append(domains, "domain:"+domain)
		}
		add(object{"inboundTag": []string{tunTag}, "domain": domains}, target)
	}
	if len(list.IPs) > 0 {
		add(object{"inboundTag": []string{tunTag}, "ip": list.IPs}, target)
	}
	if len(list.Processes) > 0 {
		add(object{"inboundTag": []string{tunTag}, "process": list.Processes}, target)
	}
	add(object{"inboundTag": []string{tunTag}, "network": "tcp,udp"}, final)
	nativeRoute["rules"] = rules
	nativeRoute["domainStrategy"] = "IPIfNonMatch"
	return cfg, nil
}
