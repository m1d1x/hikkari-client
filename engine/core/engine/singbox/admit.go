package singbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	configfile "NekkoVPN/core/engine/singbox/config"
	vpn "NekkoVPN/core/engine/singbox/process"
)

// The core decodes its whole config before it starts anything and exits over
// the first field it does not know, so one profile it refuses takes every
// other profile down with it (tester-002, 04.11: one Amnezia profile, and an
// old VLESS profile that had worked stopped starting too). Before a start the
// core's own check reads the config; a profile it refuses is left out of the
// config and the rest start without it.

// rejectedConfig is the core's refusal of a config, in its own words.
type rejectedConfig struct{ detail string }

func (e *rejectedConfig) Error() string { return e.detail }

// configChecker reads the config at path as the core's `run` would. A
// *rejectedConfig is the core's answer; any other error means the check could
// not be made.
type configChecker func(ctx context.Context, path string) error

// coreCheckTimeout bounds one check: it decodes and builds the box without
// starting it, tens of milliseconds on a working machine.
const coreCheckTimeout = 15 * time.Second

func checkWithCore(binary string) configChecker {
	return func(ctx context.Context, path string) error {
		if _, err := os.Stat(binary); err != nil {
			return fmt.Errorf("no core at %s", binary)
		}
		ctx, cancel := context.WithTimeout(ctx, coreCheckTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, "check", "-c", path)
		cmd.Dir = filepath.Dir(binary) // libcronet.dll sits beside the core
		hideWindow(cmd)
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		err := cmd.Run()
		if err == nil {
			return nil
		}
		var exit *exec.ExitError
		if ctx.Err() != nil || !errors.As(err, &exit) {
			return fmt.Errorf("core check: %w", err)
		}
		return &rejectedConfig{detail: vpn.CoreReason(out.String(), path)}
	}
}

// blamedBlock finds the block the core named: "endpoints[0].peers[0]...",
// "outbounds[3].tls..." from the decoder, "initialize endpoint[0]: ..." from
// the box.
var blamedBlock = regexp.MustCompile(`\b(endpoint|outbound)s?\[(\d+)\]`)

// compiled is what the last Configure wrote, kept to write it again without a
// profile the core refused.
type compiled struct {
	specs   []configfile.ProfileSpec
	labels  map[string]string // tag -> profile label
	names   map[string]string // tag -> profile name
	auto    bool
	rebuild func(specs []configfile.ProfileSpec, active string) error
}

// admit checks the written config with the core and leaves out every profile
// the core refuses, until the core takes the config. The active profile is not
// left out unless the selection is automatic: the user asked for that server,
// and starting another one would hide why it does not work.
func (b *Backend) admit(ctx context.Context) error {
	// Kept until the next start: a profile switch rewrites the config with every
	// profile, but the running core still lacks the ones left out.
	b.rejected = map[string]error{}
	if b.check == nil || b.compiled == nil {
		return nil
	}
	path := b.config.Path()
	for range len(b.compiled.specs) + 1 {
		err := b.check(ctx, path)
		if err == nil {
			return nil
		}
		var rejected *rejectedConfig
		if !errors.As(err, &rejected) {
			// The start goes on: the core reports the same refusal itself.
			b.log.Logf("sing-box: config not checked: %v", err)
			return nil
		}
		tag := b.blamedProfile(rejected.detail)
		if tag == "" {
			return fmt.Errorf("ядро не приняло конфиг: %s", rejected.detail)
		}
		label := b.compiled.labels[tag]
		refusal := fmt.Errorf("профиль «%s» не принят ядром: %s", label, rejected.detail)
		b.log.Logf("sing-box: profile %s left out, the core refused it: %s", tag, rejected.detail)
		b.rejected[b.compiled.names[tag]] = refusal
		if tag == b.active && !b.compiled.auto {
			return refusal
		}
		kept := make([]configfile.ProfileSpec, 0, len(b.compiled.specs)-1)
		for _, s := range b.compiled.specs {
			if s.Tag != tag {
				kept = append(kept, s)
			}
		}
		if len(kept) == 0 {
			return refusal
		}
		active := b.active
		if active == tag {
			active = kept[0].Tag
		}
		if err := b.compiled.rebuild(kept, active); err != nil {
			return err
		}
		b.compiled.specs, b.active = kept, active
	}
	return errors.New("ядро не приняло конфиг")
}

// blamedProfile is the tag of the profile the core's refusal names, or "" when
// it names something that is not a profile.
func (b *Backend) blamedProfile(detail string) string {
	m := blamedBlock.FindStringSubmatch(detail)
	if m == nil {
		return ""
	}
	index, err := strconv.Atoi(m[2])
	if err != nil {
		return ""
	}
	cfg, err := b.config.Read()
	if err != nil {
		return ""
	}
	blocks, _ := cfg[m[1]+"s"].([]interface{})
	if index >= len(blocks) {
		return ""
	}
	block, _ := blocks[index].(map[string]interface{})
	tag, _ := block["tag"].(string)
	if _, ok := b.compiled.labels[tag]; !ok {
		return ""
	}
	return tag
}
