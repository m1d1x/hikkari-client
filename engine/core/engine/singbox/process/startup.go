//go:build windows || darwin

package vpn

import (
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

// startSettle bounds how long Start waits for a core it launched to answer.
// A config the core refuses ends it in well under a second; a core that is
// merely slow (rule sets, a busy machine) is let through when the budget runs
// out, and the GUI's own watch over the first seconds still sees it die later.
const startSettle = 5 * time.Second

// startPoll is how often Start asks whether the core is up.
const startPoll = 100 * time.Millisecond

// ReadyWhen makes Start wait until ready reports the core up, or the core
// exits, which Start then reports with the core's own last words. Without it
// Start returns as soon as the process is launched: a core that dies on its
// config was then reported running (tester-002, 04.11: "ok" at start, the
// core already gone).
func (m *ProcessManager) ReadyWhen(ready func() bool) *ProcessManager {
	m.ready = ready
	return m
}

// supervise copies the core's output into the log, keeps its tail for the
// reason it gives when it exits, and closes the returned channel once it has
// exited. Wait runs only after both pipes are drained: it closes them, and
// would cut off the last lines, which are the reason.
func (m *ProcessManager) supervise(cmd *exec.Cmd, stdout, stderr io.ReadCloser, outTag, errTag string) (<-chan struct{}, *outputTail) {
	tail := &outputTail{}
	var pipes sync.WaitGroup
	pipe := func(tag string, r io.ReadCloser) {
		if r == nil {
			return
		}
		pipes.Add(1)
		go func() {
			defer pipes.Done()
			m.log.PipeLines(tag, readCloser{io.TeeReader(r, tail), r})
		}()
	}
	pipe(outTag, stdout)
	pipe(errTag, stderr)
	done := make(chan struct{})
	go func() {
		pipes.Wait()
		err := cmd.Wait()
		m.log.Logf("sing-box exited: err=%v, state=%v", err, cmd.ProcessState)
		close(done)
	}()
	return done, tail
}

// awaitStart waits for the launched core to come up (see ReadyWhen).
func (m *ProcessManager) awaitStart(done <-chan struct{}, tail *outputTail) error {
	if m.ready == nil {
		return nil
	}
	settle := time.NewTimer(startSettle)
	defer settle.Stop()
	poll := time.NewTicker(startPoll)
	defer poll.Stop()
	for {
		select {
		case <-done:
			return fmt.Errorf("ядро остановилось при запуске: %s", tail.reason(m.configPath))
		case <-settle.C:
			m.log.Logf("vpn.Start: core not answering after %s, leaving it running", startSettle)
			return nil
		case <-poll.C:
			if m.ready() {
				return nil
			}
		}
	}
}

type readCloser struct {
	io.Reader
	io.Closer
}

// outputTailSize keeps a few of the core's last lines: enough for its fatal
// one, which comes last.
const outputTailSize = 8 << 10

// outputTail is the end of what the core wrote to stdout and stderr.
type outputTail struct {
	mu  sync.Mutex
	buf []byte
}

func (t *outputTail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - outputTailSize; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
	}
	return len(p), nil
}

// reason is the core's own explanation of its exit (CoreReason).
func (t *outputTail) reason(configPath string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return CoreReason(string(t.buf), configPath)
}
