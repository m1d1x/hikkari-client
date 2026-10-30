package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// subscriptionDeviceID is stable across refreshes and application upgrades.
// Use a random installation ID, never the host's serial number or MAC address.
func (c *Core) subscriptionDeviceID() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	path := filepath.Join(c.baseDir, "subscription-device-id")
	data, err := os.ReadFile(path)
	if err == nil {
		id := strings.TrimSpace(string(data))
		decoded, err := hex.DecodeString(id)
		if err != nil || len(decoded) != 16 {
			return "", fmt.Errorf("повреждён идентификатор устройства подписки")
		}
		return id, nil
	}
	if !os.IsNotExist(err) {
		return "", fmt.Errorf("чтение идентификатора подписки: %w", err)
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("создание идентификатора подписки: %w", err)
	}
	id := hex.EncodeToString(random[:])
	if err := os.MkdirAll(c.baseDir, 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(id+"\n"), 0600); err != nil {
		return "", fmt.Errorf("сохранение идентификатора подписки: %w", err)
	}
	return id, nil
}
