package netdoctor

import (
	"net/http"
	"sync"
	"time"
)

// DirectPath is what the direct path actually reaches right now, measured
// with real requests rather than inferred from the machine's settings.
type DirectPath struct {
	// Checked is false when no probe could run; the rest is then empty.
	Checked bool `json:"checked"`
	// Internet: at least one ordinary site answered.
	Internet bool `json:"internet"`
	// SecureDNSBlocked names the public DoH resolvers that do not answer
	// although ordinary sites do. A browser in Secure DNS mode using one of
	// them resolves nothing without the tunnel.
	SecureDNSBlocked []string `json:"secureDnsBlocked"`
}

// The targets are neutral on purpose: never our servers, so a check cannot
// expose them. They are HTTPS because the core's probe ignores plain HTTP URLs,
// and any HTTP answer counts, so the paths need not exist.
var (
	directSites    = []string{"https://ya.ru/", "https://vk.com/"}
	secureDNSProbe = []struct{ name, url string }{
		{"Cloudflare", "https://chrome.cloudflare-dns.com/dns-query"},
		{"Google", "https://dns.google/dns-query"},
	}
)

// DirectProbeTimeout bounds one request; all of them run at once.
const DirectProbeTimeout = 4 * time.Second

// ProbeDirectPath runs the requests through fetch, which returns nil when the
// target answered at all. Only the user pressing «проверить» runs it.
func ProbeDirectPath(fetch func(url string) error) DirectPath {
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		res     = DirectPath{Checked: true, SecureDNSBlocked: []string{}}
		blocked = map[string]bool{}
	)
	for _, site := range directSites {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if fetch(site) == nil {
				mu.Lock()
				res.Internet = true
				mu.Unlock()
			}
		}()
	}
	for _, p := range secureDNSProbe {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if fetch(p.url) != nil {
				mu.Lock()
				blocked[p.name] = true
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	// With no internet at all a silent resolver says nothing about it.
	if res.Internet {
		for _, p := range secureDNSProbe {
			if blocked[p.name] {
				res.SecureDNSBlocked = append(res.SecureDNSBlocked, p.name)
			}
		}
	}
	return res
}

// FetchDirect is fetch for a machine with the tunnel down: an ordinary request
// over the system's own network. It ignores the system proxy, which the doctor
// judges separately.
func FetchDirect(url string) error {
	client := &http.Client{
		Timeout:   DirectProbeTimeout,
		Transport: &http.Transport{DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Head(url)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}
