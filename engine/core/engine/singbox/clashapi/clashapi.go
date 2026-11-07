// Package clashapi is a tiny HTTP client for the sing-box Clash API,
// exposed locally at experimental.clash_api.external_controller (the
// engine's config pins this to 127.0.0.1:9090). We use it for:
//
//   - SelectProxy: instant profile switch without restarting the core.
//   - Delay: end-to-end probe through a specific proxy. This is the
//     real-world signal the uptime tracker uses — "did a request from
//     this machine succeed all the way to a foreign endpoint, through
//     this protocol, just now."
//   - Proxy, Connections: what the core already knows. Both are answered from
//     its memory, so reading them costs no network at all.
package clashapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is the engine's Clash API binding from config.json
// (experimental.clash_api.external_controller). Hardcoded here because we
// own both ends of this pipe.
const DefaultBaseURL = "http://127.0.0.1:9090"

// DefaultDelayURL is the canonical Android-style connectivity probe — a
// tiny 204 No Content that's free to fetch. Hosted by Cloudflare, used
// here because Google's equivalent gstatic endpoint is frequently messed
// with by DPI in countries with active censorship.
const DefaultDelayURL = "http://cp.cloudflare.com/generate_204"

// Client is a thin HTTP wrapper. Construct with New(); operations are
// safe to call from multiple goroutines.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// New builds a client with sensible timeouts. Pass "" to use DefaultBaseURL.
func New(baseURL string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		BaseURL: baseURL,
		HTTP:    &http.Client{Timeout: 5 * time.Second},
	}
}

// IsAlive returns true when GET /version answers 200 within 600ms — same
// liveness check core/vpn.ClashAPIRunning() uses, just bound to this
// client's BaseURL.
func (c *Client) IsAlive() bool {
	req, _ := http.NewRequest("GET", c.BaseURL+"/version", nil)
	cl := &http.Client{Timeout: 600 * time.Millisecond}
	resp, err := cl.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// SelectProxy switches a selector-group outbound to the named member.
// `group` is the selector's tag (we use "proxy"); `member` is one of the
// tags listed in the selector's outbounds. Returns nil on success.
func (c *Client) SelectProxy(group, member string) error {
	body, _ := json.Marshal(map[string]string{"name": member})
	req, err := http.NewRequest(
		http.MethodPut,
		c.BaseURL+"/proxies/"+url.PathEscape(group),
		bytes.NewReader(body),
	)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("clashapi: select %q→%q: %w", group, member, err)
	}
	defer resp.Body.Close()
	// 204 No Content is the success response.
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("clashapi: select %q→%q: %s: %s", group, member, resp.Status, b)
	}
	return nil
}

// DelayResult mirrors the Clash API response shape.
type DelayResult struct {
	Delay int    `json:"delay"`
	Error string `json:"message"`
}

// Delay measures the round-trip time of a probe request through the named
// proxy. The engine fetches `probeURL` through `tag`, reports the latency
// in milliseconds. Errors from the proxy (server down, DPI block, timeout)
// surface as a non-nil error — that's the "protocol broken right now"
// signal the UI shows as a red point.
//
// probeURL and timeoutMs may be zero — sensible defaults are used.
func (c *Client) Delay(tag, probeURL string, timeoutMs int) (int, error) {
	if probeURL == "" {
		probeURL = DefaultDelayURL
	}
	if timeoutMs <= 0 {
		timeoutMs = 2000
	}
	query := url.Values{"url": {probeURL}, "timeout": {strconv.Itoa(timeoutMs)}}
	u := c.BaseURL + "/proxies/" + url.PathEscape(tag) + "/delay?" + query.Encode()
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	cl := &http.Client{Timeout: time.Duration(timeoutMs+1000) * time.Millisecond}
	resp, err := cl.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("clashapi: delay %q: %s: %s", tag, resp.Status, b)
	}
	var out DelayResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, fmt.Errorf("clashapi: delay %q: decode: %w", tag, err)
	}
	if out.Error != "" {
		return 0, fmt.Errorf("%s", out.Error)
	}
	return out.Delay, nil
}

// ProxyInfo is the part of GET /proxies/{name} we read. Now is set on groups;
// History holds the outbound's last probe and is empty once a probe failed.
type ProxyInfo struct {
	Now     string `json:"now"`
	History []struct {
		Delay int `json:"delay"`
	} `json:"history"`
}

// Proxy reads one outbound or group. A missing tag is an error, which is how
// the auto group answers while auto mode is off.
func (c *Client) Proxy(tag string) (ProxyInfo, error) {
	var out ProxyInfo
	err := c.getJSON("/proxies/"+url.PathEscape(tag), &out)
	return out, err
}

// Connection is the part of one GET /connections entry we read. Chains lists
// the outbounds it passes through, the leaf first.
type Connection struct {
	ID       string   `json:"id"`
	Upload   uint64   `json:"upload"`
	Download uint64   `json:"download"`
	Chains   []string `json:"chains"`
	Start    string   `json:"start"`
	Metadata struct {
		Network  string `json:"network"`
		DestPort string `json:"destinationPort"`
		Host     string `json:"host"`
	} `json:"metadata"`
}

// Connections lists the open connections.
func (c *Client) Connections() ([]Connection, error) {
	var out struct {
		Connections []Connection `json:"connections"`
	}
	err := c.getJSON("/connections", &out)
	return out.Connections, err
}

func (c *Client) getJSON(path string, out interface{}) error {
	return c.getJSONContext(context.Background(), path, out)
}

func (c *Client) getJSONContext(ctx context.Context, path string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("clashapi: %s: %s", path, resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("clashapi: %s: decode: %w", path, err)
	}
	return nil
}

// LookupIPv4 resolves host through the running core's DNS (GET /dns/query).
// A query from the API carries no inbound, so it skips the tunnel's fake-ip
// rule and lands on dns-direct: the real address, as the core itself would
// resolve an outbound's server.
func (c *Client) LookupIPv4(host string) ([]net.IP, error) {
	var out struct {
		Status int `json:"Status"`
		Answer []struct {
			Type int    `json:"type"`
			Data string `json:"data"`
		} `json:"Answer"`
	}
	if err := c.getJSON("/dns/query?type=A&name="+url.QueryEscape(host), &out); err != nil {
		return nil, err
	}
	var ips []net.IP
	for _, answer := range out.Answer {
		if answer.Type != 1 { // A
			continue
		}
		if ip := net.ParseIP(strings.TrimSpace(answer.Data)); ip != nil {
			ips = append(ips, ip)
		}
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("clashapi: no A record for %s (rcode %d)", host, out.Status)
	}
	return ips, nil
}

// Totals includes completed connections, unlike summing the open connection list.
func (c *Client) Totals() (upload, download uint64, err error) {
	var out struct {
		Upload   uint64 `json:"uploadTotal"`
		Download uint64 `json:"downloadTotal"`
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	err = c.getJSONContext(ctx, "/connections", &out)
	return out.Upload, out.Download, err
}

// SetPreference is optional on older cores. They keep their normal failover.
func (c *Client) SetPreference(tags []string, expiresAt int64) (bool, error) {
	raw, _ := json.Marshal(struct {
		Outbounds []string `json:"outbounds"`
		ExpiresAt int64    `json:"expiresAt"`
	}{tags, expiresAt})
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.BaseURL+"/proxies/auto/hikkari-preference", bytes.NewReader(raw))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 || resp.StatusCode == 405 {
		return false, nil
	}
	if resp.StatusCode != 204 {
		return true, fmt.Errorf("local capacity advice: HTTP %d", resp.StatusCode)
	}
	return true, nil
}
