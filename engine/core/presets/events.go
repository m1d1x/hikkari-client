package presets

import (
	"bufio"
	"context"
	"fmt"
	"mime"
	"net/http"
	"strings"
	"time"
)

func (m *Manager) runEvents(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		started := time.Now()
		if m.Snapshot().SyncEnabled {
			_ = m.listen(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
		if time.Since(started) > time.Minute {
			backoff = time.Second
		}
		if !m.Snapshot().SyncEnabled {
			backoff = time.Second
		}
	}
}

func (m *Manager) listen(parent context.Context) error {
	// Bounded lifetime prevents a silent half-open stream from living forever;
	// periodic refresh independently reconciles missed invalidations.
	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	defer cancel()
	m.mu.Lock()
	m.streamCancel = cancel
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.connected = false; m.streamCancel = nil; m.mu.Unlock() }()
	if !m.Snapshot().SyncEnabled {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.options.EventsURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	client := *m.options.Client
	client.Timeout = 0
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if resp.StatusCode != 200 || mediaType != "text/event-stream" {
		return fmt.Errorf("некорректный SSE ответ")
	}
	m.mu.Lock()
	m.connected = true
	m.mu.Unlock()
	// Reconcile after each reconnect; events lost during downtime are harmless.
	select {
	case m.wake <- struct{}{}:
	default:
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 4096), 64*1024)
	hasData := false
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if strings.HasPrefix(line, "data:") {
			hasData = true
		}
		if line == "" && hasData {
			select {
			case m.wake <- struct{}{}:
			default:
			}
			hasData = false
		}
	}
	return scanner.Err()
}
