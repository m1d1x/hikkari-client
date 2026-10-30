// Package clashapi is a tiny HTTP client for the sing-box Clash API,
// exposed locally at experimental.clash_api.external_controller (the
// engine's config pins this to 127.0.0.1:9090). We use it for two things:
//
//   - SelectProxy: instant profile switch without restarting the core.
//   - Delay: end-to-end probe through a specific proxy. This is the
//     real-world signal the uptime tracker uses — "did a request from
//     this machine succeed all the way to a foreign endpoint, through
//     this protocol, just now."
package clashapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
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
