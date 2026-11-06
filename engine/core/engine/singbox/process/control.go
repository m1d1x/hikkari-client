//go:build windows || darwin

package vpn

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// The hot reload control channel of the patched core (core-patches 0009,
// cmd_run_hikkari.go): the core started with --hikkari-control reads
// "hikkari:reload <sha256>" from stdin, applies the config file in place when
// only proxy outbounds and group members changed, and answers with one
// "hikkari:reload-result {json}" line on stdout. stdin belongs to this process,
// so unlike the loopback Clash API nobody else on the machine can send it.
const (
	controlFlag         = "--hikkari-control"
	controlReloadPrefix = "hikkari:reload "
	controlResultPrefix = "hikkari:reload-result "
)

// ReloadResult is the core's answer. Applied=false with Declined set means the
// config needs a restart; the running core is exactly as before.
type ReloadResult struct {
	Hash     string   `json:"hash"`
	Applied  bool     `json:"applied"`
	Declined string   `json:"declined,omitempty"`
	Error    string   `json:"error,omitempty"`
	Added    []string `json:"added,omitempty"`
	Removed  []string `json:"removed,omitempty"`
	Replaced []string `json:"replaced,omitempty"`
	Groups   []string `json:"groups,omitempty"`
}

// ErrReloadUnsupported: the running core has no control channel (an xray
// core, or a sing-box without patch 0009).
var ErrReloadUnsupported = errors.New("ядро не умеет применять конфиг на ходу")

// control is one running core's channel. results carries the answers the
// stdout filter picks out; one reload runs at a time (reloadMu).
type control struct {
	reloadMu sync.Mutex
	stdin    io.WriteCloser
	results  chan ReloadResult
}

func newControl(stdin io.WriteCloser) *control {
	return &control{stdin: stdin, results: make(chan ReloadResult, 4)}
}

// filter returns stdout without the control answers, which it hands to
// results instead. Everything else passes through line by line.
func (c *control) filter(stdout io.ReadCloser, log LineLogger) io.ReadCloser {
	reader, writer := io.Pipe()
	go func() {
		defer stdout.Close()
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64<<10), 1<<20)
		for scanner.Scan() {
			line := scanner.Text()
			if payload, isResult := strings.CutPrefix(line, controlResultPrefix); isResult {
				var result ReloadResult
				if err := json.Unmarshal([]byte(payload), &result); err != nil {
					log.Logf("vpn.reload: unreadable answer from the core: %v", err)
					continue
				}
				select {
				case c.results <- result:
				default:
					log.Logf("vpn.reload: answer for %s arrived with nobody waiting", shortReloadHash(result.Hash))
				}
				continue
			}
			if _, err := writer.Write([]byte(line + "\n")); err != nil {
				return
			}
		}
		writer.CloseWithError(scanner.Err())
	}()
	return readCloser{reader, multiCloser{reader, stdout}}
}

type multiCloser []io.Closer

func (m multiCloser) Close() error {
	var errs []error
	for _, closer := range m {
		errs = append(errs, closer.Close())
	}
	return errors.Join(errs...)
}

// reload sends one command and waits for its answer. A late answer to an
// earlier, timed-out command is skipped by its hash.
func (c *control) reload(hash string, timeout time.Duration, exited <-chan struct{}) (ReloadResult, error) {
	c.reloadMu.Lock()
	defer c.reloadMu.Unlock()
	for drained := false; !drained; {
		select {
		case <-c.results:
		default:
			drained = true
		}
	}
	if _, err := io.WriteString(c.stdin, controlReloadPrefix+hash+"\n"); err != nil {
		return ReloadResult{}, fmt.Errorf("команда ядру не ушла: %w", err)
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case result := <-c.results:
			if result.Hash != hash {
				continue
			}
			return result, nil
		case <-exited:
			return ReloadResult{}, errors.New("ядро остановилось, не ответив на команду")
		case <-deadline.C:
			return ReloadResult{}, fmt.Errorf("ядро не ответило на команду за %s", timeout)
		}
	}
}

// Reload asks the running core to apply its config file, which must be the
// file whose sha256 is hash. ErrReloadUnsupported when the core cannot.
func (m *ProcessManager) Reload(hash string, timeout time.Duration) (ReloadResult, error) {
	m.mu.Lock()
	alive, channel, exited := m.isAlive(), m.control, m.done
	m.mu.Unlock()
	if !alive {
		return ReloadResult{}, errors.New("ядро не запущено")
	}
	if channel == nil {
		return ReloadResult{}, ErrReloadUnsupported
	}
	return channel.reload(hash, timeout, exited)
}

// controlCommand is the core's command line, with the control flag when the
// binary has it. The flag is asked of the binary itself: the same manager
// runs xray, and an updated service may briefly meet an older core.
func controlCommand(binPath, configPath string) (args []string, withControl bool) {
	args = []string{"run", "-c", configPath}
	if !binarySupportsControl(binPath) {
		return args, false
	}
	return append(args, controlFlag), true
}

// controlProbeTimeout bounds `run --help`; a binary that hangs on it has no
// control channel as far as the start is concerned.
const controlProbeTimeout = 5 * time.Second

var controlSupport struct {
	sync.Mutex
	known map[string]bool
}

// binarySupportsControl runs `<bin> run --help` once per binary file (path,
// size and modification time) and looks for the flag.
func binarySupportsControl(binPath string) bool {
	info, err := os.Stat(binPath)
	if err != nil {
		return false
	}
	key := fmt.Sprintf("%s|%d|%d", binPath, info.Size(), info.ModTime().UnixNano())
	controlSupport.Lock()
	defer controlSupport.Unlock()
	if supported, known := controlSupport.known[key]; known {
		return supported
	}
	ctx, cancel := context.WithTimeout(context.Background(), controlProbeTimeout)
	defer cancel()
	probe := exec.CommandContext(ctx, binPath, "run", "--help")
	hideConsole(probe)
	output, _ := probe.Output()
	supported := strings.Contains(string(output), strings.TrimPrefix(controlFlag, "--"))
	if controlSupport.known == nil {
		controlSupport.known = make(map[string]bool)
	}
	controlSupport.known[key] = supported
	return supported
}

func shortReloadHash(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}
