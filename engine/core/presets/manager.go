package presets

import (
	"NekkoVPN/core/fsutil"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"time"

	"NekkoVPN/core/routing"
)

// These may be supplied by release ldflags. No request is made to a made-up
// domain before the production service exists.
var CatalogURL, EventsURL string

type State struct {
	SyncEnabled bool    `json:"syncEnabled"`
	Catalog     Catalog `json:"catalog"`
	Choices     Choices `json:"choices"`
	Source      string  `json:"source"`
	// Bucket places the install in a staged rollout: 0-99, drawn once from
	// this install's own randomness and sent with each catalog request. It
	// is not the telemetry id and says nothing about the device.
	Bucket *int `json:"bucket,omitempty"`
}

type Snapshot struct {
	State
	Configured  bool   `json:"configured"`
	Platform    string `json:"platform"`
	LastChecked string `json:"lastChecked"`
	Error       string `json:"error"`
	Connected   bool   `json:"connected"`
}

type Options struct {
	URL       string
	EventsURL string
	Client    *http.Client
	// Apply is called before publishing state. On failure the old state is
	// kept, and Apply is called with the old state to restore the backend.
	Apply func(State) error
	// Transact owns application, persistence and rollback as one application
	// transaction. It is mutually exclusive with the legacy Apply callback.
	Transact func(old, next State, persist func() error) error
}

type Manager struct {
	mu                           sync.RWMutex
	tx                           sync.Mutex
	state                        State
	path                         string
	options                      Options
	lastChecked, lastError, etag string
	connected                    bool
	wake                         chan struct{}
	streamCancel                 context.CancelFunc
}

func New(path string, options Options) (*Manager, error) {
	if options.Apply != nil && options.Transact != nil {
		return nil, fmt.Errorf("выберите один обработчик транзакции пресетов")
	}
	for _, endpoint := range []string{options.URL, options.EventsURL} {
		if endpoint != "" {
			if err := validateEndpoint(endpoint); err != nil {
				return nil, err
			}
		}
	}
	if options.EventsURL != "" && options.URL == "" {
		return nil, fmt.Errorf("SSE требует URL каталога")
	}
	if options.Client == nil {
		options.Client = &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("слишком много перенаправлений")
			}
			return validateEndpoint(req.URL.String())
		}}
	}
	catalog, err := Parse(bundled)
	if err != nil {
		return nil, err
	}
	m := &Manager{path: path, options: options, wake: make(chan struct{}, 1), state: State{SyncEnabled: true, Catalog: catalog, Choices: Choices{}, Source: "bundled"}}
	if path != "" {
		data, err := os.ReadFile(path)
		if err == nil {
			var cached State
			if err = json.Unmarshal(data, &cached); err == nil {
				raw, _ := json.Marshal(cached.Catalog)
				cached.Catalog, err = Parse(raw)
			}
			if err == nil && validChoices(cached.Choices) {
				if cached.Choices == nil {
					cached.Choices = Choices{}
				}
				// A newer shipped catalog replaces only the bundled fallback.
				// Remote caches and explicit per-mode choices stay authoritative.
				if cached.Source == "bundled" && cached.Catalog.Version < catalog.Version {
					cached.Catalog = catalog
				}
				m.state = cached
				if m.state.Source == "remote" {
					m.state.Source = "cache"
				}
			} else {
				m.lastError = "Локальный кэш повреждён; используется встроенный каталог"
			}
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	if m.state.Bucket == nil {
		// Kept at once: a bucket redrawn on every start would hop the
		// install in and out of a rollout. If the write fails, the next
		// commit keeps it.
		bucket := rand.IntN(100)
		m.state.Bucket = &bucket
		if err := m.persist(m.state); err != nil {
			m.lastError = "не удалось сохранить настройки пресетов"
		}
	}
	return m, nil
}

func validateEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("некорректный URL пресетов")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && !(u.Scheme == "http" && ip != nil && ip.IsLoopback()) {
		return fmt.Errorf("пресеты требуют HTTPS (HTTP разрешён только на loopback для тестов)")
	}
	return nil
}

func validChoices(c Choices) bool {
	for mode, entries := range c {
		if mode != routing.ModeBypass && mode != routing.ModeProxy {
			return false
		}
		for id := range entries {
			if !validID.MatchString(id) {
				return false
			}
		}
	}
	return true
}

func clone(s State) State {
	data, _ := json.Marshal(s)
	var out State
	_ = json.Unmarshal(data, &out)
	return out
}

func (m *Manager) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return Snapshot{State: clone(m.state), Configured: m.options.URL != "", Platform: runtime.GOOS, LastChecked: m.lastChecked, Error: m.lastError, Connected: m.connected}
}

func (m *Manager) SetSync(on bool) error {
	m.tx.Lock()
	defer m.tx.Unlock()
	old := m.Snapshot().State
	next := clone(old)
	next.SyncEnabled = on
	if err := m.commit(old, next); err != nil {
		return err
	}
	m.mu.Lock()
	if m.streamCancel != nil {
		m.streamCancel()
	}
	m.mu.Unlock()
	select {
	case m.wake <- struct{}{}:
	default:
	}
	return nil
}

func (m *Manager) Select(mode routing.RoutingMode, id string, on bool) error {
	m.tx.Lock()
	defer m.tx.Unlock()
	if mode != routing.ModeBypass && mode != routing.ModeProxy {
		return fmt.Errorf("неизвестный режим")
	}
	old := m.Snapshot().State
	found := false
	for _, s := range old.Catalog.Services {
		if s.ID == id {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("сервис %q не найден", id)
	}
	next := clone(old)
	if next.Choices[mode] == nil {
		next.Choices[mode] = map[string]bool{}
	}
	next.Choices[mode][id] = on
	return m.commit(old, next)
}

func (m *Manager) persist(s State) error {
	if m.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.path), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(m.path), ".presets-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return fsutil.Replace(f.Name(), m.path)
}

// Caller holds tx. Readers continue seeing old state until persistence and
// backend application both succeed. No network I/O occurs under this lock.
func (m *Manager) commit(old, next State) error {
	if reflect.DeepEqual(old, next) {
		return nil
	}
	if m.options.Transact != nil {
		if err := m.options.Transact(clone(old), clone(next), func() error { return m.persist(next) }); err != nil {
			return err
		}
		m.mu.Lock()
		m.state = next
		m.mu.Unlock()
		return nil
	}
	if m.options.Apply != nil {
		if err := m.options.Apply(clone(next)); err != nil {
			restoreErr := m.options.Apply(clone(old))
			return errors.Join(err, restoreErr)
		}
	}
	// Persist only a policy the backend accepted. A crash during application
	// leaves the previous working catalog on disk, not an untested candidate.
	if err := m.persist(next); err != nil {
		if m.options.Apply != nil {
			return errors.Join(err, m.options.Apply(clone(old)))
		}
		return err
	}
	m.mu.Lock()
	m.state = next
	m.mu.Unlock()
	return nil
}

func (m *Manager) report(err error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastChecked = time.Now().UTC().Format(time.RFC3339)
	if err == nil {
		m.lastError = ""
	} else {
		m.lastError = err.Error()
	}
	return err
}

// Refresh fetches outside tx; the final transaction rechecks opt-out/version,
// so an in-flight response can never undo the user's disabled sync setting.
func (m *Manager) Refresh(ctx context.Context) error {
	if !m.Snapshot().SyncEnabled || m.options.URL == "" {
		return nil
	}
	endpoint, err := url.Parse(m.options.URL)
	if err != nil {
		return m.report(err)
	}
	query := endpoint.Query()
	query.Set("bucket", fmt.Sprint(*m.Snapshot().Bucket))
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return m.report(err)
	}
	m.mu.RLock()
	etag := m.etag
	m.mu.RUnlock()
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := m.options.Client.Do(req)
	if err != nil {
		return m.report(fmt.Errorf("не удалось обновить пресеты: %w", err))
	}
	defer resp.Body.Close()
	// 204: nothing is released for this install yet; it keeps its catalog.
	if resp.StatusCode == http.StatusNotModified || resp.StatusCode == http.StatusNoContent {
		return m.report(nil)
	}
	if resp.StatusCode != http.StatusOK {
		return m.report(fmt.Errorf("сервер пресетов: HTTP %d", resp.StatusCode))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxCatalogBytes+1))
	if err != nil {
		return m.report(err)
	}
	catalog, err := Parse(data)
	if err != nil {
		return m.report(err)
	}
	m.tx.Lock()
	defer m.tx.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	old := m.Snapshot().State
	if !old.SyncEnabled {
		return nil
	}
	if catalog.Version < old.Catalog.Version {
		return m.report(fmt.Errorf("сервер вернул старую версию пресетов"))
	}
	if catalog.Version == old.Catalog.Version && !reflect.DeepEqual(catalog, old.Catalog) {
		return m.report(fmt.Errorf("содержимое каталога изменено без увеличения версии"))
	}
	next := clone(old)
	next.Catalog = catalog
	next.Source = "remote"
	if err := m.commit(old, next); err != nil {
		return m.report(fmt.Errorf("не удалось применить пресеты: %w", err))
	}
	m.mu.Lock()
	m.etag = resp.Header.Get("ETag")
	m.mu.Unlock()
	return m.report(nil)
}

// Run owns polling and SSE lifetimes. SSE invalidates the cache; the actual
// document always comes from the same validated HTTP refresh path.
func (m *Manager) Run(ctx context.Context) {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if m.options.EventsURL != "" {
		go m.runEvents(streamCtx)
	}
	_ = m.Refresh(ctx)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = m.Refresh(ctx)
		case <-m.wake:
			_ = m.Refresh(ctx)
		}
	}
}
