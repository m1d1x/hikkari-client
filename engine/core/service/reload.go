package service

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	vpn "NekkoVPN/core/engine/singbox/process"
	"NekkoVPN/core/svcproto"
)

// reloadTimeout bounds one in-place reload. The core builds and starts the
// new outbounds before it answers; that takes milliseconds, a TLS-less setup
// of each node. A core that does not answer in time is restarted by the GUI.
const reloadTimeout = 15 * time.Second

// reload applies a new sing-box config to the running core without a restart
// (svcproto.OpReload). The config is sanitised and stored as for a start; the
// core then reads it and swaps the outbounds that changed. Unless the core
// applied it, the stored config, its hash and version are put back exactly as
// they were, so what the service reports always matches what the core runs.
// Callers hold mu.
func (s *Server) reload(req svcproto.Request) (svcproto.ReloadOutcome, error) {
	declined := func(reason string) (svcproto.ReloadOutcome, error) {
		s.log.Logf("service: reload declined: %s", reason)
		return svcproto.ReloadOutcome{Declined: reason}, nil
	}
	if req.Engine != "" && req.Engine != svcproto.EngineSingBox {
		return declined("engine " + req.Engine + " restarts on every change")
	}
	if engine, pid := s.running(); engine != svcproto.EngineSingBox || pid == 0 {
		return declined("core is not running")
	}
	if s.awaitingConfig() {
		return declined("core waits for this release's config")
	}
	engine := svcproto.EngineSingBox
	previous, err := s.snapshotConfig(engine)
	if err != nil {
		return svcproto.ReloadOutcome{}, err
	}
	if err := s.writeConfig(engine, req.Config, req.ConfigHash); err != nil {
		return svcproto.ReloadOutcome{}, err
	}
	written, err := os.ReadFile(s.configPath(engine))
	if err != nil {
		return svcproto.ReloadOutcome{}, errors.Join(err, s.restoreConfig(engine, previous))
	}
	sum := sha256.Sum256(written)
	hash := hex.EncodeToString(sum[:])
	result, err := s.reloadCore(engine, hash)
	if err == nil && result.Applied {
		s.log.Logf("service: core reloaded in place: added=%d removed=%d replaced=%d groups=%s hash=%s",
			len(result.Added), len(result.Removed), len(result.Replaced), strings.Join(result.Groups, ","), shortHash(req.ConfigHash))
		return svcproto.ReloadOutcome{
			Applied:  true,
			Added:    result.Added,
			Removed:  result.Removed,
			Replaced: result.Replaced,
			Groups:   result.Groups,
		}, nil
	}
	if restoreErr := s.restoreConfig(engine, previous); restoreErr != nil {
		return svcproto.ReloadOutcome{}, fmt.Errorf("конфиг не восстановлен после отказа ядра: %w", restoreErr)
	}
	switch {
	case errors.Is(err, vpn.ErrReloadUnsupported):
		return declined("core without hot reload")
	case err != nil:
		return declined(err.Error())
	case result.Error != "":
		return declined("core failed: " + result.Error)
	default:
		return declined(result.Declined)
	}
}

// reloadCore is the core's control channel; a test replaces the core binary,
// not this.
func (s *Server) reloadCore(engine, hash string) (vpn.ReloadResult, error) {
	return s.cores[engine].Reload(hash, reloadTimeout)
}

// storedConfig is the config, hash and version files as they were.
type storedConfig map[string][]byte

func (s *Server) snapshotConfig(engine string) (storedConfig, error) {
	stored := storedConfig{}
	for _, path := range []string{s.configPath(engine), s.configHashPath(engine), s.configVersionPath(engine)} {
		content, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			stored[path] = nil
			continue
		}
		if err != nil {
			return nil, err
		}
		stored[path] = content
	}
	return stored, nil
}

func (s *Server) restoreConfig(engine string, stored storedConfig) error {
	var errs []error
	for path, content := range stored {
		if content == nil {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, err)
			}
			continue
		}
		errs = append(errs, writeAtomic(path, content))
	}
	return errors.Join(errs...)
}
