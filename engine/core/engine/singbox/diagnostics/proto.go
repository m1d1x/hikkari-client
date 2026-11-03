package diag

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	vpn "NekkoVPN/core/engine/singbox/process"
)

// The operator's second question: the address opens, but does the protocol? Each
// profile runs in its own sing-box beside the tunnel, bound to the physical card,
// listening only on loopback, with no TUN and no route changes, so the user's
// tunnel is not touched. The config lives in a private temp directory for the
// length of one try. Lines carry labels and counts only: profiles hold secrets.

const (
	maxProtoTargets = 5
	protoTries      = 3
	protoStartWait  = 5 * time.Second
	protoTryTimeout = 5 * time.Second
	protoCheckURL   = "http://cp.cloudflare.com/generate_204"
)

var protoOutbounds = map[string]bool{"vless": true, "vmess": true, "trojan": true, "shadowsocks": true, "hysteria2": true, "tuic": true}

type protoTarget struct {
	Label    string                 `json:"label"`
	Outbound map[string]interface{} `json:"outbound"`
	// ExitURL is the hub's address: called through the profile, it shows the
	// hub where this service lets traffic out.
	ExitURL string `json:"exit_url"`
}

func parseProtoTargets(params json.RawMessage) ([]protoTarget, error) {
	var req struct {
		Targets []protoTarget `json:"targets"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, errors.New("params: not readable") // the decoder's text can quote a secret
	}
	if len(req.Targets) == 0 || len(req.Targets) > maxProtoTargets {
		return nil, fmt.Errorf("want 1 to %d targets, got %d", maxProtoTargets, len(req.Targets))
	}
	for _, t := range req.Targets {
		if !probeLabel.MatchString(t.Label) {
			return nil, errors.New("bad label")
		}
		if t.ExitURL != "" && !strings.HasPrefix(t.ExitURL, "https://") {
			return nil, fmt.Errorf("target %s: bad exit url", t.Label)
		}
		kind, _ := t.Outbound["type"].(string)
		if !protoOutbounds[kind] {
			return nil, fmt.Errorf("target %s: outbound type not allowed", t.Label)
		}
		server, _ := t.Outbound["server"].(string)
		if server == "" {
			return nil, fmt.Errorf("target %s: no server", t.Label)
		}
		if ip, err := netip.ParseAddr(server); err == nil {
			public := ip.Is4() && ip.IsGlobalUnicast() && !ip.IsPrivate()
			for _, p := range probeNotPublic {
				public = public && !p.Contains(ip)
			}
			if !public {
				return nil, fmt.Errorf("target %s: not a public address", t.Label)
			}
		}
	}
	return req.Targets, nil
}

// protoConfig is the whole config of one try.
func protoConfig(outbound map[string]interface{}, iface string, port int) ([]byte, error) {
	out := make(map[string]interface{}, len(outbound)+2)
	for k, v := range outbound {
		out[k] = v
	}
	out["tag"] = "probe"
	out["bind_interface"] = iface
	return json.Marshal(map[string]interface{}{
		"log":       map[string]interface{}{"disabled": true},
		"inbounds":  []interface{}{map[string]interface{}{"type": "mixed", "listen": "127.0.0.1", "listen_port": port}},
		"outbounds": []interface{}{out},
		"route":     map[string]interface{}{"final": "probe"},
	})
}

// ProtoTargets answers the hub's proto_targets with «label proto=2/3 med=210ms».
func ProtoTargets(ctx context.Context, binary string, params json.RawMessage) []string {
	targets, err := parseProtoTargets(params)
	if err != nil {
		return []string{fmt.Sprintf("error=%q", err.Error())}
	}
	// Like the flow counts: only here does a socket bound to the card leave past
	// the TUN, and an answer through the tunnel would prove nothing.
	if runtime.GOOS != "windows" {
		return []string{fmt.Sprintf("error=%q", "needs Windows, this is "+runtime.GOOS)}
	}
	ifaces := physicalIfaces()
	if len(ifaces) == 0 {
		return []string{`error="no physical interface"`}
	}
	var lines []string
	for _, t := range targets {
		if ctx.Err() != nil {
			return append(lines, `error="out of time"`)
		}
		lines = append(lines, protoLine(ctx, binary, t, ifaces[0].Name))
	}
	return lines
}

func protoLine(ctx context.Context, binary string, t protoTarget, iface string) string {
	opened, med, err := tryProfile(ctx, binary, t.Outbound, t.ExitURL, iface)
	if err != nil {
		// err is ours and names no profile field; the core's own output is dropped.
		return fmt.Sprintf("%s proto_error=%q", t.Label, err.Error())
	}
	return fmt.Sprintf("%s proto=%d/%d med=%s", t.Label, opened, protoTries, med)
}

func tryProfile(ctx context.Context, binary string, outbound map[string]interface{}, exitURL, iface string) (int, string, error) {
	proxy, stop, err := startProfile(ctx, binary, outbound, iface)
	if err != nil {
		return 0, "", err
	}
	defer stop()
	client := profileClient(proxy, protoTryTimeout)
	opened, med := countRequests(ctx, client, protoCheckURL)
	if opened > 0 && exitURL != "" {
		if resp, err := client.Get(exitURL); err == nil {
			resp.Body.Close()
		}
	}
	return opened, med, nil
}

// startProfile runs one profile in its own core and returns the core's local
// proxy. stop ends the core and removes the config, which holds the secrets.
func startProfile(ctx context.Context, binary string, outbound map[string]interface{}, iface string) (*url.URL, func(), error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, errors.New("no free port")
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	config, err := protoConfig(outbound, iface, port)
	if err != nil {
		return nil, nil, errors.New("config")
	}
	dir, err := os.MkdirTemp("", "hikkari-proto-")
	if err != nil {
		return nil, nil, errors.New("temp dir")
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, config, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return nil, nil, errors.New("write config")
	}
	runCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(runCtx, binary, "run", "-c", path)
	cmd.Dir = filepath.Dir(binary) // libcronet.dll sits beside the core
	hideWindow(cmd)                // the GUI has no console, and a console program would open one
	if err := cmd.Start(); err != nil {
		cancel()
		_ = os.RemoveAll(dir)
		return nil, nil, errors.New("core did not start")
	}
	vpn.DieWithUs(cmd.Process.Pid)
	stop := func() {
		cancel()
		_ = cmd.Wait()
		_ = os.RemoveAll(dir)
	}
	if !waitPort(runCtx, port) {
		stop()
		return nil, nil, errors.New("core rejected the profile")
	}
	proxy, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	return proxy, stop, nil
}

// profileClient opens a new connection through the proxy for every request:
// one profile, as many source ports as requests, as apps do.
func profileClient(proxy *url.URL, timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}}
}

func waitPort(ctx context.Context, port int) bool {
	deadline := time.Now().Add(protoStartWait)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// countRequests counts answers of the check URL: a captive portal's 200 is not
// a working tunnel, only the 204 is.
func countRequests(ctx context.Context, client *http.Client, target string) (int, string) {
	var took []time.Duration
	for i := 0; i < protoTries && ctx.Err() == nil; i++ {
		start := time.Now()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			break
		}
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusNoContent {
			took = append(took, time.Since(start))
		}
	}
	if len(took) == 0 {
		return 0, "-"
	}
	sort.Slice(took, func(i, j int) bool { return took[i] < took[j] })
	return len(took), took[len(took)/2].Round(time.Millisecond).String()
}
