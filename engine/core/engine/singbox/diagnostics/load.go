package diag

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	vpn "NekkoVPN/core/engine/singbox/process"
)

// The reach ping is one small request on a quiet path, and a profile can pass it
// while nothing works on it: amsterdam-1-002 answered in 110 ms all day and every
// app on it hung (plans/2026-11-02-tcp-flow-loss.md). This round asks what apps
// ask of a profile. Each one runs in its own core beside the tunnel, as in
// proto.go, so the user's traffic is not touched and a dead profile can be
// measured without being selected.
//
// The steps run in this order on purpose. The download goes first, on a quiet
// path: it stops short if a flow is cut by volume. The burst is what an app opens
// when it comes up: it fails if the number of connections is the problem. The
// last requests show whether the path came back after the burst or stayed shut.
//
// About 1 MB and 50 connections per profile, so tester mode only and not often.
//
// Before any of it, the address is asked whether it answers at all (addr.go): on
// a banned address every profile setting reads red and says nothing, and 50
// connections to it are only more noise. On a mobile network the burst is small
// (mobile.go): parallel connections there can shut the whole network down until
// the phone reconnects.
const (
	loadEvery     = 30 * time.Minute
	loadBytes     = 1 << 20
	loadBurst     = 50
	loadBurstWait = 15 * time.Second // the core's default TLS handshake deadline
	loadDownWait  = 20 * time.Second
)

// loadPlan is one round's targets and limits.
type loadPlan struct {
	DownloadURL string
	CheckURL    string
	Bytes       int64
	Burst       int
	BurstWait   time.Duration
	DownWait    time.Duration
	AfterWait   time.Duration
	// Local is the card's address the address check leaves from; nil skips it.
	Local net.IP
}

var defaultLoadPlan = loadPlan{
	DownloadURL: fmt.Sprintf("https://speed.cloudflare.com/__down?bytes=%d", loadBytes),
	CheckURL:    protoCheckURL,
	Bytes:       loadBytes,
	Burst:       loadBurst,
	BurstWait:   loadBurstWait,
	DownWait:    loadDownWait,
	AfterWait:   protoTryTimeout,
}

// loadTarget is one profile as the core's config holds it, secrets included:
// Outbound never reaches a log line.
type loadTarget struct {
	Tag      string
	Proto    string
	Outbound map[string]interface{}
}

// loadTargets lists the profiles a core of their own can run. A detour names an
// outbound this core would not have, and WireGuard is an endpoint, not an outbound.
func loadTargets(config []byte) ([]loadTarget, error) {
	var cfg struct {
		Outbounds []map[string]interface{} `json:"outbounds"`
	}
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, errors.New("not readable") // the decoder's text can quote a secret
	}
	listed, err := reachTargets(config)
	if err != nil {
		return nil, errors.New("not readable")
	}
	protos := make(map[string]string, len(listed))
	for _, t := range listed {
		protos[t.Tag] = t.Proto
	}
	var out []loadTarget
	for _, o := range cfg.Outbounds {
		tag, _ := o["tag"].(string)
		kind, _ := o["type"].(string)
		if !strings.HasPrefix(tag, "p-") || !protoOutbounds[kind] || o["detour"] != nil {
			continue
		}
		delete(o, "domain_resolver") // names a server of the tunnel's config
		out = append(out, loadTarget{Tag: tag, Proto: protos[tag], Outbound: o})
	}
	return out, nil
}

// maybeLoad starts the round when it is allowed and due. It runs apart from the
// session's loop: at up to a minute per profile it would stop the periodic lines.
func (r *Runner) maybeLoad(ctx context.Context) {
	r.mu.Lock()
	allowed := r.reachAllowed
	due := !r.loadBusy && time.Since(r.loadAt) >= loadEvery
	ifaces := r.ifaces
	r.mu.Unlock()
	if allowed == nil || !due || !allowed() {
		return
	}
	r.mu.Lock()
	r.loadAt = time.Now()
	r.loadBusy = true
	r.mu.Unlock()
	go func() {
		defer func() {
			r.mu.Lock()
			r.loadBusy = false
			r.mu.Unlock()
		}()
		r.loadRound(ctx, vpn.CoreBinary(), ifaces)
	}()
}

func (r *Runner) loadRound(ctx context.Context, binary string, ifaces []physIface) {
	targets, skipped := r.prepareLoad(ifaces)
	if skipped != "" {
		r.log.Logf("[diag:load] %s", skipped)
		return
	}
	r.runLoad(ctx, binary, targets, ifaces[0])
}

// loadOS is runtime.GOOS; a test stands in for Windows.
var loadOS = runtime.GOOS

// prepareLoad reads the profiles of a round, or says why there is none.
func (r *Runner) prepareLoad(ifaces []physIface) ([]loadTarget, string) {
	// As in reach.go: only here does a socket bound to the card leave past the TUN.
	if loadOS != "windows" {
		return nil, "skipped: needs Windows, this is " + loadOS
	}
	if len(ifaces) == 0 {
		return nil, "skipped: no physical interface"
	}
	config, err := os.ReadFile(r.configPath)
	if err != nil {
		return nil, fmt.Sprintf("config: %v", err)
	}
	targets, err := loadTargets(config)
	if err != nil {
		return nil, fmt.Sprintf("config: %v", err)
	}
	return targets, ""
}

func (r *Runner) runLoad(ctx context.Context, binary string, targets []loadTarget, iface physIface) {
	plan := defaultLoadPlan
	plan.Local = iface.IP
	network := loadSelfNet(iface.IP, r.stunList())
	mobile := onMobile(iface.Name, network)
	round := newWireRound(ctx, binary, network)
	r.mu.Lock()
	r.loadMobile = mobile
	r.mu.Unlock()
	if mobile {
		plan.Burst = loadBurstMobile
	}
	for _, t := range targets {
		if ctx.Err() != nil {
			return
		}
		// The hello first: it needs no network and says what the counts below
		// were measured with (wire.go).
		r.log.Logf("[diag:wire] %s", wireLine(ctx, binary, t, round))
		r.log.Logf("[diag:load] %s", loadLine(ctx, binary, t, iface.Name, plan))
	}
}

// loadNowWait bounds a round on request: about a minute a profile.
const loadNowWait = 15 * time.Minute

// LoadNow answers the hub's load_profiles: a round now, tunnel up or down, so
// the operator does not wait up to half an hour for the next one. It returns at
// once, since a round outlasts a command's two minutes. The profiles' lines
// follow as [diag:load], closed by «on request: done», and the next periodic
// round counts from now.
func (r *Runner) LoadNow() []string {
	return r.loadNow(vpn.CoreBinary(), physicalIfaces())
}

func (r *Runner) loadNow(binary string, ifaces []physIface) []string {
	targets, skipped := r.prepareLoad(ifaces)
	if skipped != "" {
		return []string{fmt.Sprintf("error=%q", skipped)}
	}
	r.mu.Lock()
	if r.loadBusy {
		r.mu.Unlock()
		return []string{`error="a round is running, its lines follow"`}
	}
	// A request does not get past the mobile network's limit of one round in
	// loadEvery: a burst there can shut the tester's whole network.
	if left := loadEvery - time.Since(r.loadAt); r.loadMobile && left > 0 {
		r.mu.Unlock()
		return []string{fmt.Sprintf(`error="mobile network: next round in %s"`, left.Round(time.Minute))}
	}
	r.loadAt = time.Now()
	r.loadBusy = true
	r.mu.Unlock()
	go func() {
		defer func() {
			r.mu.Lock()
			r.loadBusy = false
			r.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), loadNowWait)
		defer cancel()
		r.runLoad(ctx, binary, targets, ifaces[0])
		r.log.Logf("[diag:load] on request: done")
	}()
	return []string{fmt.Sprintf("started profiles=%d", len(targets))}
}

// loadLine carries the tag, the knobs the profile ran with and the counts. The
// knobs (loadKnobs) are what the hub's relay dataset charts a round by; no
// address and no secret is among them.
func loadLine(ctx context.Context, binary string, t loadTarget, iface string, plan loadPlan) string {
	head := t.Tag + " proto=" + t.Proto + loadKnobs(t.Outbound)
	if tcp, dead := addrDead(ctx, t, plan.Local); dead {
		return fmt.Sprintf("%s addr_dead=1 tcp=%d/%d reply=0/%d", head, tcp, addrFlows, addrFlows)
	}
	proxy, stop, err := startProfile(ctx, binary, t.Outbound, iface)
	if err != nil {
		// err is ours and names no profile field; the core's own output is dropped.
		return fmt.Sprintf("%s load_error=%q", head, err.Error())
	}
	defer stop()
	got, took, end := download(ctx, profileClient(proxy, 0), plan.DownloadURL, plan.DownWait)
	ok, timedOut, med := burst(ctx, profileClient(proxy, plan.BurstWait), plan.CheckURL, plan.Burst)
	after, afterMed := countRequests(ctx, profileClient(proxy, plan.AfterWait), plan.CheckURL)
	return fmt.Sprintf("%s dl=%d/%d took=%s end=%s burst=%d/%d timeout=%d med=%s after=%d/%d med=%s",
		head, got, plan.Bytes, took.Round(time.Millisecond), end,
		ok, plan.Burst, timedOut, med, after, protoTries, afterMed)
}

// loadKnobs are the config knobs of an outbound the relay search varies
// (hikkari-client plans/2026-11-04-relay-search-method.md): fp, sni, port, flow
// and alpn, as " key=value" in that order, each only when the outbound sets it.
// The server name is a cover domain, which the log keeps; the port is not an
// address. A value with anything but plain characters is left out, so the
// line stays one token per knob.
func loadKnobs(ob map[string]interface{}) string {
	var b strings.Builder
	add := func(k, v string) {
		if v == "" || strings.IndexFunc(v, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._-/:,", r))
		}) >= 0 {
			return
		}
		b.WriteString(" " + k + "=" + v)
	}
	tls, _ := ob["tls"].(map[string]interface{})
	if u, _ := tls["utls"].(map[string]interface{}); u != nil {
		if on, _ := u["enabled"].(bool); on {
			fp, _ := u["fingerprint"].(string)
			add("fp", fp)
		}
	}
	sni, _ := tls["server_name"].(string)
	add("sni", sni)
	switch p := ob["server_port"].(type) {
	case float64:
		add("port", fmt.Sprintf("%d", int(p)))
	case int:
		add("port", fmt.Sprintf("%d", p))
	}
	if typ, _ := ob["type"].(string); typ == "vless" {
		flow, _ := ob["flow"].(string)
		if flow == "" {
			flow = "none"
		}
		add("flow", flow)
	}
	if list, _ := tls["alpn"].([]interface{}); len(list) > 0 {
		parts := make([]string, 0, len(list))
		for _, v := range list {
			if s, ok := v.(string); ok {
				parts = append(parts, s)
			}
		}
		add("alpn", strings.Join(parts, "/"))
	}
	return b.String()
}

// download reads the URL to its end and says how far it got and how it ended:
// ok, timeout (the flow went silent), closed (it was cut), or the HTTP status.
func download(ctx context.Context, client *http.Client, target string, wait time.Duration) (int64, time.Duration, string) {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, 0, "closed"
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, time.Since(start), loadEnd(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, time.Since(start), fmt.Sprintf("http%d", resp.StatusCode)
	}
	got, err := io.Copy(io.Discard, resp.Body)
	if err != nil {
		return got, time.Since(start), loadEnd(err)
	}
	return got, time.Since(start), "ok"
}

func loadEnd(err error) string {
	if isTimeout(err) {
		return "timeout"
	}
	return "closed"
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.As(err, &ne) && ne.Timeout()
}

// burst sends n requests at once, each on its own connection, and counts the
// answers of the check URL and the requests that ran out of time.
func burst(ctx context.Context, client *http.Client, target string, n int) (ok, timedOut int, median string) {
	type result struct {
		took    time.Duration
		ok      bool
		timeout bool
	}
	results := make(chan result, n)
	for i := 0; i < n; i++ {
		go func() {
			start := time.Now()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
			if err != nil {
				results <- result{}
				return
			}
			resp, err := client.Do(req)
			if err != nil {
				results <- result{timeout: isTimeout(err)}
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			results <- result{took: time.Since(start), ok: resp.StatusCode == http.StatusNoContent}
		}()
	}
	var took []time.Duration
	for i := 0; i < n; i++ {
		res := <-results
		if res.ok {
			took = append(took, res.took)
		}
		if res.timeout {
			timedOut++
		}
	}
	if len(took) == 0 {
		return 0, timedOut, "-"
	}
	sort.Slice(took, func(i, j int) bool { return took[i] < took[j] })
	return len(took), timedOut, took[len(took)/2].Round(time.Millisecond).String()
}
