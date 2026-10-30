// Package singbox implements the engine contract using sing-box. All native
// configuration, outbound tags, Clash API and process supervision live here.
package singbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"runtime"
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
}

func New(baseDir string, log engine.Logger) (engine.Backend, error) {
	return NewWithProcess(baseDir, log, vpn.NewProcessManager(filepath.Join(baseDir, vpn.BinaryName), filepath.Join(baseDir, "config.json"), baseDir, log)), nil
}
func NewWithProcess(baseDir string, log engine.Logger, process vpn.Manager) *Backend {
	return &Backend{config: configfile.New(filepath.Join(baseDir, "config.json")), process: process, control: clashapi.New(""), diagnostics: diag.New(log, filepath.Join(baseDir, "config.json")), log: log}
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
		specs = append(specs, configfile.ProfileSpec{Tag: tag, Kind: n.Kind, Config: cfg})
		if p.Name == c.ActiveProfile {
			active = tag
		}
	}
	if active == "" && len(specs) > 0 {
		active = specs[0].Tag
	}
	return b.config.RebuildActiveWithRouting(specs, active, c.AutoSelect, legacyTags, c.Routing)
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
	vpn.FlushDNS()
	results, err := b.config.PinServerIPs(func(host string) ([]net.IP, error) {
		lookup, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return net.DefaultResolver.LookupIP(lookup, "ip4", host)
	})
	if err != nil {
		b.log.Logf("sing-box: pin server IPs: %v", err)
	}
	for _, r := range results {
		if r.Err != nil {
			b.log.Logf("sing-box: server %s (%s) not pinned: %v", r.Host, r.Tag, r.Err)
		} else {
			b.log.Logf("sing-box: server %s (%s) pinned to %s", r.Host, r.Tag, r.IP)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := b.process.Start(); err != nil {
		return err
	}
	b.diagnostics.AfterStart(ctx)
	return nil
}
func (b *Backend) Stop() error {
	b.diagnostics.Stop()
	if err := b.process.Stop(); err != nil {
		return err
	}
	b.stoppedAt = time.Now()
	return nil
}
func (b *Backend) IsRunning() bool { return b.control.IsAlive() }
func (b *Backend) SelectProfile(id string) error {
	return b.control.SelectProxy(selectorTag, profileTag(id))
}
func (b *Backend) Delay(id, url string, timeoutMs int) (int, error) {
	return b.control.Delay(profileTag(id), url, timeoutMs)
}
func (b *Backend) Baseline() { b.diagnostics.Baseline() }
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
