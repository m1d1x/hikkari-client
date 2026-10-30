package xray

import (
	"NekkoVPN/core/fsutil"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"NekkoVPN/core/engine"
	"NekkoVPN/core/hostprocess"
	"NekkoVPN/core/profile"
	"NekkoVPN/core/profile/legacy"
)

type Host interface {
	Start(context.Context) error
	Stop() error
	IsRunning() bool
}
type Backend struct {
	mu                sync.Mutex
	dir, path, binary string
	log               engine.Logger
	host              Host
	ports             map[string]int
	active            string
	newHost           func(string, string, map[string]int) Host
}

func New(dir string, log engine.Logger) (engine.Backend, error) {
	if runtime.GOOS != "darwin" {
		return nil, fmt.Errorf("адаптер Xray пока поддержан только на macOS")
	}
	b := &Backend{dir: dir, path: filepath.Join(dir, "xray.json"), log: log, ports: map[string]int{}}
	if raw, err := os.ReadFile(filepath.Join(dir, "xray-runtime.json")); err == nil {
		if err := json.Unmarshal(raw, &b.ports); err != nil {
			return nil, fmt.Errorf("повреждено состояние портов Xray: %w", err)
		}
		used := map[int]bool{}
		for _, port := range b.ports {
			if port < 1 || port > 65535 || used[port] {
				return nil, fmt.Errorf("повреждены локальные порты Xray")
			}
			used[port] = true
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if b.ports == nil {
		b.ports = map[string]int{}
	}
	b.host = &hostprocess.Daemon{Name: "xray", Directory: dir, Args: []string{"run", "-c", b.path}, Ready: func() bool { return listenersReady(b.ports) }, DNSAddress: "172.31.7.1"}
	return b, nil
}
func (b *Backend) Info() engine.Info {
	return engine.Info{ID: "xray", Name: "Xray " + Version, Capabilities: engine.Capabilities{TUN: runtime.GOOS == "darwin", DomainRouting: true, IPRouting: true, AutoSelect: true, ProfileProbe: true, ProcessRouting: true}}
}
func (b *Backend) Configure(c engine.Configuration) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := engine.ValidateConfiguration(c, b.Info().Capabilities); err != nil {
		return err
	}
	previous := b.ports
	if b.host == nil || !b.host.IsRunning() {
		previous = nil
	}
	ports, err := AllocatePorts(c.Profiles, previous)
	if err != nil {
		return err
	}
	cfg, err := CompileVPN(c, ports)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := writeConfig(b.path, raw); err != nil {
		return err
	}
	state, err := json.Marshal(ports)
	if err != nil {
		return err
	}
	if err := writeConfig(filepath.Join(b.dir, "xray-runtime.json"), state); err != nil {
		return err
	}
	b.ports = ports
	b.active = c.ActiveProfile
	return nil
}
func writeConfig(path string, raw []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".xray-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(raw); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return fsutil.Replace(tmp, path)
}
func (b *Backend) Start(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.host != nil && b.host.IsRunning() {
		return nil
	}
	if len(b.ports) == 0 {
		return fmt.Errorf("Xray: сначала добавьте сервер")
	}
	binary, err := ResolveBinary(b.dir)
	if err != nil {
		return err
	}
	check, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	version, err := exec.CommandContext(check, binary, "version").CombinedOutput()
	if err != nil || !strings.HasPrefix(string(version), "Xray "+Version+" ") {
		return fmt.Errorf("требуется Xray %s", Version)
	}
	output, err := exec.CommandContext(check, binary, "run", "-test", "-c", b.path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("Xray отклонил конфигурацию: %s", strings.TrimSpace(string(output)))
	}
	b.binary = binary
	// Resolve server names before system DNS is redirected into this process.
	if err := b.pinEndpoints(ctx); err != nil {
		return err
	}
	ports := make(map[string]int, len(b.ports))
	for id, port := range b.ports {
		ports[id] = port
	}
	var host Host
	if b.newHost != nil {
		host = b.newHost(binary, b.path, ports)
	} else {
		host = &hostprocess.Daemon{Name: "xray", Binary: binary, Directory: b.dir, Args: []string{"run", "-c", b.path}, Ready: func() bool { return listenersReady(ports) }, DNSAddress: "172.31.7.1"}
	}
	b.host = host
	if err := host.Start(ctx); err != nil {
		cleanup := host.Stop()
		if b.log != nil {
			raw, _ := os.ReadFile(filepath.Join(b.dir, "xray.log"))
			if len(raw) > 8192 {
				raw = raw[len(raw)-8192:]
			}
			b.log.Logf("[engine.stderr] %s", raw)
		}
		return fmt.Errorf("%w (остановка: %v)", err, cleanup)
	}
	return nil
}
func listenersReady(ports map[string]int) bool {
	if len(ports) == 0 {
		return false
	}
	for _, port := range ports {
		c, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", port), 150*time.Millisecond)
		if err != nil {
			return false
		}
		_ = c.SetDeadline(time.Now().Add(150 * time.Millisecond))
		_, err = c.Write([]byte{5, 1, 0})
		var response [2]byte
		if err == nil {
			_, err = io.ReadFull(c, response[:])
		}
		_ = c.Close()
		if err != nil || response != [2]byte{5, 0} {
			return false
		}
	}
	return true
}
func (b *Backend) pinEndpoints(ctx context.Context) error {
	raw, err := os.ReadFile(b.path)
	if err != nil {
		return err
	}
	var cfg object
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return err
	}
	outs, _ := cfg["outbounds"].([]interface{})
	for _, item := range outs {
		ob := item.(map[string]interface{})
		if !strings.HasPrefix(fmt.Sprint(ob["tag"]), "transport-") {
			continue
		}
		settings, _ := ob["settings"].(map[string]interface{})
		endpoints := []map[string]interface{}{}
		for _, key := range []string{"vnext", "servers"} {
			if values, ok := settings[key].([]interface{}); ok {
				for _, value := range values {
					endpoints = append(endpoints, value.(map[string]interface{}))
				}
			}
		}
		if _, ok := settings["address"]; ok {
			endpoints = append(endpoints, settings)
		}
		for _, ep := range endpoints {
			host, _ := ep["address"].(string)
			if net.ParseIP(host) != nil {
				continue
			}
			lookup, cancel := context.WithTimeout(ctx, 5*time.Second)
			ips, e := net.DefaultResolver.LookupIP(lookup, "ip4", host)
			cancel()
			if e != nil || len(ips) == 0 {
				return fmt.Errorf("Xray: не удалось разрешить адрес сервера %q", host)
			}
			ep["address"] = ips[0].String()
		}
	}
	encoded, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return writeConfig(b.path, encoded)
}
func (b *Backend) Stop() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.host == nil {
		return nil
	}
	return b.host.Stop()
}
func (b *Backend) IsRunning() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.host != nil && b.host.IsRunning()
}
func (b *Backend) SelectProfile(string) error {
	return fmt.Errorf("Xray: выбор профиля применяется общим механизмом перезапуска")
}
func (b *Backend) Delay(id, url string, timeout int) (int, error) {
	b.mu.Lock()
	port, ok := b.ports[id]
	host := b.host
	b.mu.Unlock()
	if !ok {
		return 0, fmt.Errorf("профиль не найден")
	}
	if host == nil || !host.IsRunning() {
		return 0, fmt.Errorf("Xray не запущен")
	}
	return Probe(port, url, timeout)
}
func (b *Backend) Decode(input string) ([]profile.Profile, error)       { return Decode(input) }
func (b *Backend) Normalize(p profile.Profile) (profile.Profile, error) { return legacy.Normalize(p) }
func (b *Backend) Export(p profile.Profile) (string, error) {
	if p.Connection != nil {
		return p.Connection.URI, nil
	}
	return legacy.ExportURI(p)
}
func (b *Backend) Baseline() {}
func (b *Backend) Diagnose(reason string) {
	if b.log != nil {
		b.log.Logf("[xray] %s: running=%v, log=%s", reason, b.IsRunning(), filepath.Join(b.dir, "xray.log"))
	}
}
