//go:build windows

package netdoctor

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const internetSettingsKey = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

// WinINet option codes that tell running programs the settings changed, so
// a browser or Steam's CEF picks up the disabled proxy without a restart.
const (
	internetOptionSettingsChanged = 39
	internetOptionRefresh         = 37
)

var (
	wininet                   = windows.NewLazySystemDLL("wininet.dll")
	procInternetSetOption     = wininet.NewProc("InternetSetOptionW")
	dnsapi                    = windows.NewLazySystemDLL("dnsapi.dll")
	procDnsFlushResolverCache = dnsapi.NewProc("DnsFlushResolverCache")
)

func readProxy() (ProxySetting, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsKey, registry.QUERY_VALUE)
	if err != nil {
		return ProxySetting{}, fmt.Errorf("opening Internet Settings: %w", err)
	}
	defer k.Close()
	var s ProxySetting
	if v, _, err := k.GetIntegerValue("ProxyEnable"); err == nil {
		s.Enabled = v != 0
	} else if !errors.Is(err, registry.ErrNotExist) {
		return ProxySetting{}, fmt.Errorf("reading ProxyEnable: %w", err)
	}
	s.Server, _, _ = k.GetStringValue("ProxyServer")
	s.AutoConfigURL, _, _ = k.GetStringValue("AutoConfigURL")
	return s, nil
}

// disableProxy switches ProxyEnable off and nothing else: ProxyServer stays,
// so the user can turn the same proxy back on from Windows settings.
func disableProxy() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("opening Internet Settings: %w", err)
	}
	defer k.Close()
	if err := k.SetDWordValue("ProxyEnable", 0); err != nil {
		return fmt.Errorf("writing ProxyEnable: %w", err)
	}
	notifySettingsChanged()
	return nil
}

func notifySettingsChanged() {
	if procInternetSetOption.Find() != nil {
		return
	}
	procInternetSetOption.Call(0, internetOptionSettingsChanged, 0, 0)
	procInternetSetOption.Call(0, internetOptionRefresh, 0, 0)
}

// flushDNS empties the resolver cache in-process, as `ipconfig /flushdns`
// does, without spawning a console.
func flushDNS() error {
	if err := procDnsFlushResolverCache.Find(); err != nil {
		return fmt.Errorf("DnsFlushResolverCache unavailable: %w", err)
	}
	if r, _, err := procDnsFlushResolverCache.Call(); r == 0 {
		return fmt.Errorf("DnsFlushResolverCache: %w", err)
	}
	return nil
}

// Diagnose inspects the user's network settings without changing anything.
func Diagnose() (Report, error) {
	s, err := readProxy()
	if err != nil {
		return Report{}, err
	}
	return Report{Supported: true, Proxy: Classify(s)}, nil
}

// SanitizeProxy disables the system proxy if, and only if, it is orphaned
// (see Classify). It is the zero-touch part of the doctor: the engine runs it
// before every connect, so a proxy a crashed Clash left behind does not
// break the direct traffic the tunnel lets bypass it.
func SanitizeProxy() (Action, error) {
	s, err := readProxy()
	if err != nil {
		return Action{Name: "proxy_disable"}, err
	}
	return sanitize(Classify(s)), nil
}

func sanitize(r ProxyReport) Action {
	a := Action{Name: "proxy_disable"}
	switch r.Verdict {
	case ProxyOrphan:
		// Re-read right before writing: if the user or a tool changed the
		// setting since the probe, the verdict no longer applies.
		now, err := readProxy()
		switch {
		case err != nil:
			a.Detail = err.Error()
		case now != r.Setting:
			a.Detail = "proxy setting changed during repair, left as is"
		default:
			if err := disableProxy(); err != nil {
				a.Detail = err.Error()
			} else {
				a.Done = true
				a.Detail = "disabled orphan system proxy " + r.Setting.Server
			}
		}
	case ProxyLive:
		a.Detail = "system proxy " + r.Setting.Server + " is in use, left as is"
	case ProxyRemote:
		a.Detail = "system proxy " + r.Setting.Server + " is not local, left as is"
	default:
		a.Detail = "no system proxy"
	}
	return a
}

// Repair is the soft repair: it disables an orphaned system proxy and
// flushes the DNS cache. It never needs elevation and needs no reboot.
func Repair() (RepairResult, error) {
	before, err := Diagnose()
	if err != nil {
		return RepairResult{}, err
	}
	res := RepairResult{Before: before, Actions: []Action{sanitize(before.Proxy)}}
	dns := Action{Name: "dns_flush"}
	if err := flushDNS(); err != nil {
		dns.Detail = err.Error()
	} else {
		dns.Done = true
	}
	res.Actions = append(res.Actions, dns)
	return res, nil
}
