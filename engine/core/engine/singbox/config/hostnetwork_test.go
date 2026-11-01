package configfile

import "testing"

func TestHostNetworkPinsTheInterfaceAndResolverAndUndoesThem(t *testing.T) {
	cfg := DefaultConfig()
	applyHostNetwork(cfg, HostNetwork{DirectInterface: "Wi-Fi", DirectDNS: "77.88.8.8"})
	route := cfg["route"].(map[string]interface{})
	if route["auto_detect_interface"] != false || route["default_interface"] != "Wi-Fi" {
		t.Fatalf("route: %v", route)
	}
	var direct map[string]interface{}
	for _, s := range cfg["dns"].(map[string]interface{})["servers"].([]interface{}) {
		if m := s.(map[string]interface{}); m["tag"] == "dns-direct" {
			direct = m
		}
	}
	if direct["type"] != "udp" || direct["server"] != "77.88.8.8" || direct["detour"] != nil {
		t.Fatalf("dns-direct: %v", direct)
	}

	applyHostNetwork(cfg, HostNetwork{})
	if route["auto_detect_interface"] != true {
		t.Fatalf("auto-detect not restored: %v", route)
	}
	if _, has := route["default_interface"]; has {
		t.Fatalf("default_interface left behind: %v", route)
	}
}
