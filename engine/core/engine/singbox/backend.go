// Package singbox implements the engine contract using sing-box. All native
// configuration, outbound tags, Clash API and process supervision live here.
package singbox

import (
	"NekkoVPN/core/autotunnel"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"NekkoVPN/core/engine"
	"NekkoVPN/core/engine/singbox/clashapi"
	configfile "NekkoVPN/core/engine/singbox/config"
	diag "NekkoVPN/core/engine/singbox/diagnostics"
	vpn "NekkoVPN/core/engine/singbox/process"
	"NekkoVPN/core/profile"
)

const selectorTag = "proxy"

func profileTag(id string) string { return "p-" + id }

var legacyTags = map[string]bool{"awg-ep": true, "proxy-out": true}

type Backend struct {
	config      *configfile.Store
	process     vpn.Manager
	control     *clashapi.Client
	diagnostics *diag.Runner
	log         engine.Logger
	stoppedAt   time.Time
	tester      func() bool // see SetTesterGate
	active      string      // selector member the last Configure chose
	check       configChecker
	compiled    *compiled
	rejected    map[string]error  // profile name -> why the running core was started without it
	fingerprint string            // of what the last Configure wrote, before admit and the pins
	pins        map[string]string // server host -> IPv4 the running core dials (see pinServers)
}

// selectActiveTimeout bounds the wait for a freshly started core's Clash API.
const selectActiveTimeout = 3 * time.Second

func New(baseDir string, log engine.Logger) (engine.Backend, error) {
	return NewWithProcess(baseDir, log, vpn.NewProcessManager(filepath.Join(baseDir, vpn.BinaryName), filepath.Join(baseDir, "config.json"), baseDir, log)), nil
}
func NewWithProcess(baseDir string, log engine.Logger, process vpn.Manager) *Backend {
	return &Backend{config: configfile.New(filepath.Join(baseDir, "config.json")), process: process, control: clashapi.New(""), diagnostics: diag.New(log, filepath.Join(baseDir, "config.json")), log: log, check: checkWithCore(vpn.CoreBinary())}
}
func (b *Backend) Info() engine.Info {
	return engine.Info{ID: "sing-box", Name: "sing-box", Capabilities: engine.Capabilities{TUN: runtime.GOOS == "darwin" || runtime.GOOS == "windows", DomainRouting: true, IPRouting: true, AutoSelect: true, LiveProfileSwitch: true, ProfileProbe: true, ProcessRouting: processRouting, FolderRouting: processRouting}}
}

func native(p profile.Profile) (profile.Profile, error) {
	if p.Config != nil {
		return p, nil
	} // Preserve unknown legacy fields exactly.
	if p.Connection == nil {
		return p, fmt.Errorf("профиль %q не содержит подключения", p.Label)
	}
	d, err := ParseURI(p.Connection.URI)
	if err != nil {
		return p, err
	}
	p.Kind = d.Kind
	p.Config = d.Config
	return p, nil
}
func (b *Backend) Configure(c engine.Configuration) error {
	if err := engine.ValidateConfiguration(c, b.Info().Capabilities); err != nil {
		return err
	}
	specs := make([]configfile.ProfileSpec, 0, len(c.Profiles))
	labels := make(map[string]string, len(c.Profiles))
	names := make(map[string]string, len(c.Profiles))
	active := ""
	for _, p := range c.Profiles {
		n, err := native(p)
		if err != nil {
			return fmt.Errorf("sing-box: профиль %q: %w", p.Label, err)
		}
		// Deep-copy legacy maps: compiler healing must not mutate stored profiles.
		cfg, err := cloneConfig(n.Config)
		if err != nil {
			return err
		}
		tag := profileTag(p.Name)
		cfg["tag"] = tag
		if b.tester != nil && b.tester() && wantsFlowRetry(n.Kind, cfg) {
			cfg["flow_retry_delay"] = flowRetryDelay
		}
		specs = append(specs, configfile.ProfileSpec{Tag: tag, Kind: n.Kind, Config: cfg})
		labels[tag], names[tag] = p.Label, p.Name
		if p.Name == c.ActiveProfile {
			active = tag
		}
	}
	if active == "" && len(specs) > 0 {
		active = specs[0].Tag
	}
	rebuild := func(specs []configfile.ProfileSpec, active string) error {
		return b.config.RebuildActiveWithRouting(specs, active, c.AutoSelect, legacyTags, c.Routing, configfile.HostNetwork{
			DirectInterface: c.Host.DirectInterface,
			DirectDNS:       c.Host.DirectDNS,
		})
	}
	if err := rebuild(specs, active); err != nil {
		return err
	}
	fingerprint, err := b.config.Fingerprint()
	if err != nil {
		return err
	}
	b.fingerprint = fingerprint
	b.active = active
	b.compiled = &compiled{specs: specs, labels: labels, names: names, auto: c.AutoSelect, rebuild: rebuild}
	return nil
}
func (b *Backend) Start(ctx context.Context) error {
	if !b.stoppedAt.IsZero() {
		remaining := 1500*time.Millisecond - time.Since(b.stoppedAt)
		if remaining > 0 {
			timer := time.NewTimer(remaining)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	// Before pinning: leaving a profile out writes the config again.
	if err := b.admit(ctx); err != nil {
		return err
	}
	vpn.FlushDNS()
	b.pinServers(true, func(host string) ([]net.IP, error) {
		lookup, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return net.DefaultResolver.LookupIP(lookup, "ip4", host)
	})
	if err := ctx.Err(); err != nil {
		return err
	}
	if tracked, ok := b.process.(supervisedProcess); ok {
		tracked.SetConfigHash(b.fingerprint)
	}
	if err := b.process.Start(); err != nil {
		return err
	}
	b.selectActive(ctx)
	b.diagnostics.AfterStart(ctx)
	return nil
}

// selectActive makes the running selector follow the configured profile. On
// start sing-box prefers the member stored in cache.db by an earlier live switch
// over the selector's `default`, so a profile chosen while the core was down
// would otherwise be silently ignored. A failure is only logged: the core is up.
func (b *Backend) selectActive(ctx context.Context) {
	if b.active == "" {
		return
	}
	wait, cancel := context.WithTimeout(ctx, selectActiveTimeout)
	defer cancel()
	for !b.control.IsAlive() {
		select {
		case <-wait.Done():
			b.log.Logf("sing-box: select %s: core API did not come up", b.active)
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
	if err := b.control.SelectProxy(selectorTag, b.active); err != nil {
		b.log.Logf("sing-box: select %s: %v", b.active, err)
	}
}
func (b *Backend) Stop() error {
	b.diagnostics.Stop()
	if err := b.process.Stop(); err != nil {
		return err
	}
	b.stoppedAt = time.Now()
	return nil
}

// StopForRestart implements engine.CoreSupervision.
func (b *Backend) StopForRestart() error {
	tracked, ok := b.process.(supervisedProcess)
	if !ok {
		return b.Stop()
	}
	b.diagnostics.Stop()
	if err := tracked.StopForRestart(); err != nil {
		return err
	}
	b.stoppedAt = time.Now()
	return nil
}
func (b *Backend) IsRunning() bool { return b.control.IsAlive() }
func (b *Backend) SelectProfile(id string) error {
	if err := b.rejected[id]; err != nil {
		return err
	}
	return b.control.SelectProxy(selectorTag, profileTag(id))
}
func (b *Backend) Delay(id, url string, timeoutMs int) (int, error) {
	return b.control.Delay(profileTag(id), url, timeoutMs)
}

// CanProbeDirect implements engine.DirectProber.
func (b *Backend) CanProbeDirect() bool { return b.control.IsAlive() }

// ProbeDirect implements engine.DirectProber through the direct outbound, which
// is what bypassed traffic uses: its interface and its resolver.
func (b *Backend) ProbeDirect(url string, timeoutMs int) error {
	_, err := b.control.Delay("direct", url, timeoutMs)
	return err
}

// SetTesterGate implements engine.TesterGated.
func (b *Backend) SetTesterGate(tester func() bool) {
	b.tester = tester
	b.diagnostics.SetReachGate(tester)
}

// flowRetryDelay is how long a TCP dial to a server stays silent before another
// attempt starts from a new source port (core patch 0003, m1d1x/sing-box-lx branch hikkari). Well over any
// RTT that works, so a path that answers never sees a second SYN.
const flowRetryDelay = "1s"

// udpProfileTypes have no TCP dial for a flow retry to act on.
var udpProfileTypes = map[string]bool{"hysteria": true, "hysteria2": true, "tuic": true, "wireguard": true}

func wantsFlowRetry(kind string, cfg map[string]interface{}) bool {
	kindOf, _ := cfg["type"].(string)
	return kind == profile.KindOutbound && !udpProfileTypes[kindOf]
}

// AutoTunnelList implements engine.DirectTraffic.
func (b *Backend) AutoTunnelList() string { return b.config.AutoTunnelPath() }

// DirectConnections implements engine.DirectTraffic.
func (b *Backend) DirectConnections() ([]autotunnel.Conn, error) {
	connections, err := b.control.Connections()
	if err != nil {
		return nil, err
	}
	out := make([]autotunnel.Conn, 0, len(connections))
	for _, c := range connections {
		start, err := time.Parse(time.RFC3339Nano, c.Start)
		if err != nil {
			continue
		}
		out = append(out, autotunnel.Conn{
			ID:       c.ID,
			Host:     c.Metadata.Host,
			Network:  c.Metadata.Network,
			Port:     c.Metadata.DestPort,
			Upload:   c.Upload,
			Download: c.Download,
			Direct:   len(c.Chains) > 0 && c.Chains[0] == "direct", // the outbound itself; groups follow it
			Start:    start,
		})
	}
	return out, nil
}

// RoutedProfile asks the auto group which profile it holds.
func (b *Backend) RoutedProfile() string {
	group, err := b.control.Proxy(configfile.AutoTag)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(group.Now, "p-")
}

// ReceivedBytes keeps only the connections whose chain passes through the
// profile: the core's totals also count everything routed direct, which says
// nothing about the tunnel.
func (b *Backend) ReceivedBytes(id string) (map[string]uint64, error) {
	connections, err := b.control.Connections()
	if err != nil {
		return nil, err
	}
	tag := profileTag(id)
	out := map[string]uint64{}
	for _, connection := range connections {
		if slices.Contains(connection.Chains, tag) {
			out[connection.ID] = connection.Download
		}
	}
	return out, nil
}

// LastDelay reads the probe the auto group runs on the node it holds, so the
// graph needs no second request for the same answer. With auto off the core does
// not probe on its own, and the history only holds what Delay left there.
func (b *Backend) LastDelay(id string) (bool, int, bool) {
	if b.RoutedProfile() != id {
		return false, 0, false
	}
	node, err := b.control.Proxy(profileTag(id))
	if err != nil {
		return false, 0, false
	}
	if len(node.History) == 0 {
		return false, 0, true
	}
	return true, node.History[0].Delay, true
}

// Baseline snapshots the machine at engine init. When the core is already up
// — the GUI attached to a tunnel the service kept or brought back (a GUI crash,
// a reboot) — Start is never called, so
// the session diagnostics start here instead; Stop ends them as usual.
func (b *Backend) Baseline() {
	b.diagnostics.Baseline()
	if b.IsRunning() {
		b.log.Logf("[diag] core already running at init: attaching session diagnostics")
		b.diagnostics.AfterStart(context.Background())
	}
}

// supervisedProcess is a core owned by the service (vpn.ServiceManager).
type supervisedProcess interface {
	SetConfigHash(hash string)
	StopForRestart() error
	CoreState(currentHash string) (engine.CoreState, error)
}

// CoreState implements engine.CoreSupervision.
func (b *Backend) CoreState() (engine.CoreState, error) {
	tracked, ok := b.process.(supervisedProcess)
	if !ok {
		return engine.CoreState{}, nil
	}
	return tracked.CoreState(b.fingerprint)
}

// ProbeNow measures the selected profile right now.
func (b *Backend) ProbeNow() { b.diagnostics.ProbeNow() }

// Snapshot writes the machine's state into the log right now, on request.
func (b *Backend) Snapshot() { b.diagnostics.Snapshot(b.IsRunning()) }

// LoadNow starts a load round over every profile right now, on request.
func (b *Backend) LoadNow() []string { return b.diagnostics.LoadNow() }

func (b *Backend) Diagnose(reason string) {
	if d, ok := b.process.(interface{ Diagnose(string) }); ok {
		d.Diagnose(reason)
	}
}
func (b *Backend) Export(p profile.Profile) (string, error) {
	n, err := native(p)
	if err != nil {
		return "", err
	}
	return ExportURI(n)
}
func (b *Backend) Normalize(p profile.Profile) (profile.Profile, error) {
	if p.Connection != nil {
		return p, nil
	}
	uri, err := ExportURI(p)
	if err != nil {
		return p, err
	}
	host, port := profile.PeerHostPort(p)
	protocol, _ := p.Config["type"].(string)
	p.Connection = &profile.Connection{URI: uri, Protocol: protocol, Host: host, Port: port}
	return p, nil
}
func (b *Backend) Decode(input string) ([]profile.Profile, error) {
	draft, err := ParseURI(input)
	if err != nil {
		return nil, err
	}
	p, err := b.Normalize(profile.Profile{Label: draft.Label, Kind: draft.Kind, Config: draft.Config})
	if err != nil {
		return nil, err
	}
	p.Config = nil
	p.Kind = ""
	return []profile.Profile{p}, nil
}

func cloneConfig(config map[string]interface{}) (map[string]interface{}, error) {
	raw, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	var out map[string]interface{}
	err = json.Unmarshal(raw, &out)
	return out, err
}

func (b *Backend) LegacyProfiles() ([]profile.Profile, error) {
	cfg, err := b.config.Read()
	if err != nil {
		return nil, err
	}
	eps, _ := cfg["endpoints"].([]interface{})
	if len(eps) == 0 {
		return nil, nil
	}
	ep, ok := eps[0].(map[string]interface{})
	if !ok {
		return nil, nil
	}
	return []profile.Profile{{Name: "main", Label: "Основной", Kind: profile.KindEndpoint, Config: ep}}, nil
}

func (b *Backend) ServiceScopes(profiles []profile.Profile) map[string]string {
	scopes := make(map[string]string, len(profiles))
	for _, p := range profiles {
		scopes[profileTag(p.Name)] = p.Name
	}
	return scopes
}

// processRouting: sing-box matches process_name and process_path_regex on
// every desktop OS it finds the owning process on — Linux included.
const processRouting = runtime.GOOS == "darwin" || runtime.GOOS == "windows" || runtime.GOOS == "linux"
