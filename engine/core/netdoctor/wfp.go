package netdoctor

import (
	"slices"
	"strings"
	"unicode"
)

// WFPProvider is one Windows Filtering Platform provider as FwpmProviderEnum0
// reports it.
type WFPProvider struct {
	Key         string // GUID, in any case, with or without braces
	Name        string
	ServiceName string
}

// microsoftWFPKeys are the providers Windows ships. Display names are
// localized («Брандмауэр Windows»), so on the machines we care about a name
// alone would report Windows itself as foreign.
var microsoftWFPKeys = map[string]bool{
	"10ad9216-ccde-456c-8b16-e9f04e60a90b": true, // IKEEXT
	"896aa19e-9a34-4bcb-ae79-beb9127c84b9": true, // TCP chimney offload
	"76cfcd30-3394-432d-bed3-441ae50e63c3": true, // TCP templates
	"3c6c05a9-c05c-4bb9-8338-2327814ce8bf": true, // IPsec DoS protection
	"4b153735-1049-4480-aab4-d1b9bdc03710": true, // Windows Firewall
	"decc16ca-3f33-4346-be1e-8fb4ae0f3d62": true, // Windows service hardening
	"a90296f7-46b8-4457-8f84-b05e1e9c4a1b": true, // Enterprise data protection
}

// microsoftWFPServices are the Windows services that own providers.
var microsoftWFPServices = map[string]bool{
	"bfe":         true,
	"ikeext":      true,
	"mpssvc":      true,
	"policyagent": true,
}

// microsoftWFPNames catch the built-in providers no key above covers.
var microsoftWFPNames = []string{
	"microsoft",
	"windows defender",
	"windows filtering",
	"windows firewall",
	"windows service hardening",
}

func (p WFPProvider) isMicrosoft() bool {
	if microsoftWFPKeys[strings.ToLower(strings.Trim(p.Key, "{}"))] || microsoftWFPServices[strings.ToLower(p.ServiceName)] {
		return true
	}
	name := strings.ToLower(p.Name)
	return slices.ContainsFunc(microsoftWFPNames, func(prefix string) bool {
		return strings.HasPrefix(name, prefix)
	})
}

// wfpNameLimit bounds what a provider can put into a log line.
const wfpNameLimit = 64

// ForeignWFPProviders names the providers that are not Microsoft's, for the
// report only: nothing in hikkari removes or disables them. The result is
// non-nil, so "none found" differs from "unknown".
func ForeignWFPProviders(providers []WFPProvider) []string {
	names := []string{}
	for _, p := range providers {
		if !p.isMicrosoft() {
			names = append(names, cleanProviderName(p.Name))
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// cleanProviderName makes a name written by third-party software safe for a
// single log line.
func cleanProviderName(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, name)
	name = strings.Join(strings.Fields(name), " ")
	if r := []rune(name); len(r) > wfpNameLimit {
		name = string(r[:wfpNameLimit])
	}
	if name == "" {
		return "unnamed"
	}
	return name
}
