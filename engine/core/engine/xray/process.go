package xray

import (
	"context"
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
)

type Process struct {
	mu                        sync.Mutex
	Binary, Config, Directory string
	Log                       engine.Logger
	cmd                       *exec.Cmd
	done                      chan struct{}
}

func ResolveBinary(baseDir string) (string, error) {
	name := "xray"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	paths := []string{filepath.Join(baseDir, name)}
	if exe, err := os.Executable(); err == nil {
		paths = append(paths, filepath.Join(filepath.Dir(exe), "../Resources", name), filepath.Join(filepath.Dir(exe), name))
	}
	// Bundled binary or an explicitly installed tool; no cwd-dependent resolution.
	if tool, err := exec.LookPath(name); err == nil {
		paths = append(paths, tool)
	}
	for _, p := range paths {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return filepath.Abs(p)
		}
	}
	return "", fmt.Errorf("Xray %s не найден: требуется бинарник %s в ресурсах приложения или папке данных", Version, name)
}

func (p *Process) Start(ctx context.Context, ports map[string]int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd != nil {
		select {
		case <-p.done:
			p.cmd = nil
		default:
			return nil
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	bin := p.Binary
	if bin == "" {
		var err error
		bin, err = ResolveBinary(p.Directory)
		if err != nil {
			return err
		}
	}
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	version, err := exec.CommandContext(checkCtx, bin, "version").CombinedOutput()
	if err != nil || !strings.HasPrefix(string(version), "Xray "+Version+" ") {
		return fmt.Errorf("требуется проверенная версия Xray %s", Version)
	}
	if output, err := exec.CommandContext(checkCtx, bin, "run", "-test", "-c", p.Config).CombinedOutput(); err != nil {
		// Native validation errors are useful, but never include the input URI/config.
		return fmt.Errorf("Xray отклонил конфигурацию: %s", strings.TrimSpace(string(output)))
	}
	cmd := exec.Command(bin, "run", "-c", p.Config)
	if runtime.GOOS == "darwin" {
		// The guardian terminates the transport if Flutter/Python crashes. Otherwise
		// an orphan keeps listeners and credentials alive after the owning app exits.
		cmd = exec.Command("/bin/sh", "-c", guardian, "hikkari-xray", bin, p.Config, fmt.Sprint(os.Getpid()))
	}
	cmd.Dir = p.Directory
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdout.Close()
		return err
	}
	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		_ = stderr.Close()
		return fmt.Errorf("запуск Xray: %w", err)
	}
	p.cmd = cmd
	p.done = make(chan struct{})
	done := p.done
	if p.Log != nil {
		go p.Log.PipeLines("engine.stdout", stdout)
		go p.Log.PipeLines("engine.stderr", stderr)
	} else {
		go io.Copy(io.Discard, stdout)
		go io.Copy(io.Discard, stderr)
	}
	go func() { _ = cmd.Wait(); close(done) }()
	deadline := time.Now().Add(8 * time.Second)
	for {
		select {
		case <-done:
			return fmt.Errorf("Xray завершился до готовности")
		default:
		}
		ready := true
		for _, port := range ports {
			conn, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", port), 50*time.Millisecond)
			if err != nil {
				ready = false
				break
			}
			_ = conn.Close()
		}
		if ready {
			return nil
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			// Kill the guardian with its cleanup trap, then release the lock so Wait
			// can publish process completion. Stop() will finish bounded teardown.
			_ = cmd.Process.Signal(os.Interrupt)
			return fmt.Errorf("Xray не открыл локальные SOCKS-порты за 8 секунд")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (p *Process) Done() <-chan struct{} { p.mu.Lock(); defer p.mu.Unlock(); return p.done }
func (p *Process) IsRunning() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd == nil {
		return false
	}
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}
func (p *Process) Stop() error {
	p.mu.Lock()
	cmd, done := p.cmd, p.done
	p.mu.Unlock()
	if cmd == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	default:
	}
	_ = cmd.Process.Signal(os.Interrupt)
	select {
	case <-done:
		return nil
	case <-time.After(3 * time.Second):
	}
	if err := cmd.Process.Kill(); err != nil && !strings.Contains(err.Error(), "process already finished") {
		return err
	}
	select {
	case <-done:
		return nil
	case <-time.After(2 * time.Second):
		return fmt.Errorf("Xray не завершился после остановки")
	}
}

const guardian = `BIN=$1
CFG=$2
OWNER=$3
CHILD=""
GUARD=""
cleanup() {
  trap '' INT TERM
  [ -z "$GUARD" ] || kill "$GUARD" 2>/dev/null
  [ -z "$CHILD" ] || kill -TERM "$CHILD" 2>/dev/null
  [ -z "$CHILD" ] || wait "$CHILD" 2>/dev/null
}
trap 'cleanup; exit 0' INT TERM
"$BIN" run -c "$CFG" &
CHILD=$!
(
  while kill -0 "$OWNER" 2>/dev/null; do sleep 1; done
  kill -TERM "$CHILD" 2>/dev/null
) &
GUARD=$!
wait "$CHILD"
STATUS=$?
cleanup
exit "$STATUS"
`
