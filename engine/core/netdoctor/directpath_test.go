package netdoctor

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestProbeDirectPath(t *testing.T) {
	dead := errors.New("timeout")
	cases := []struct {
		name         string
		fetch        func(string) error
		wantInternet bool
		wantBlocked  []string
	}{
		{"all answer", func(string) error { return nil }, true, []string{}},
		{"one site is enough", func(url string) error {
			if strings.Contains(url, "ya.ru") {
				return dead
			}
			return nil
		}, true, []string{}},
		{"the provider drops cloudflare", func(url string) error {
			if strings.Contains(url, "cloudflare") {
				return dead
			}
			return nil
		}, true, []string{"Cloudflare"}},
		{"both resolvers dropped", func(url string) error {
			if strings.Contains(url, "dns") {
				return dead
			}
			return nil
		}, true, []string{"Cloudflare", "Google"}},
		{"no network says nothing about the resolvers", func(string) error { return dead }, false, []string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ProbeDirectPath(c.fetch)
			if !got.Checked || got.Internet != c.wantInternet || !reflect.DeepEqual(got.SecureDNSBlocked, c.wantBlocked) {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func TestDirectProbesNeverTouchOurServers(t *testing.T) {
	var urls []string
	urls = append(urls, directSites...)
	for _, p := range secureDNSProbe {
		urls = append(urls, p.url)
	}
	for _, url := range urls {
		if !strings.HasPrefix(url, "https://") {
			t.Errorf("%s: the core's probe ignores plain HTTP", url)
		}
		for _, ours := range []string{"zovfy", "hikkari"} {
			if strings.Contains(url, ours) {
				t.Errorf("%s points at our own infrastructure", url)
			}
		}
	}
}
