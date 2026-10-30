package xray

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/net/proxy"
)

// Probe measures a real request through a specific transport's SOCKS listener.
// It does not use Clash's HTTP-URL rewriting or the selected profile group.
func Probe(port int, target string, timeoutMs int) (int, error) {
	if target == "" {
		target = "https://cp.cloudflare.com/generate_204"
	}
	u, err := url.Parse(target)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return 0, fmt.Errorf("неверный HTTP(S) URL проверки")
	}
	if timeoutMs <= 0 {
		timeoutMs = 2000
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()
	dialer, err := proxy.SOCKS5("tcp", fmt.Sprintf("127.0.0.1:%d", port), nil, &net.Dialer{Timeout: time.Duration(timeoutMs) * time.Millisecond})
	if err != nil {
		return 0, err
	}
	transport := &http.Transport{DialContext: dialer.(proxy.ContextDialer).DialContext, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, target, nil)
	if err != nil {
		return 0, err
	}
	start := time.Now()
	response, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 400 {
		return 0, fmt.Errorf("проверка вернула HTTP %d", response.StatusCode)
	}
	ms := int(time.Since(start) / time.Millisecond)
	if ms < 1 {
		ms = 1
	}
	return ms, nil
}
