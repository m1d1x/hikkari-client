package rureach

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type rig struct {
	p      *Prober
	clock  time.Time
	tester bool
	answer map[string]int // site → ms; missing: no answer
	blind  bool
	lines  []string
}

func newRig() *rig {
	r := &rig{clock: time.Unix(1_800_000_000/60*60, 0), tester: true, answer: map[string]int{Sites[0]: 31, Sites[1]: 44}}
	r.p = New(Sources{
		Enabled: func() bool { return r.tester },
		Fetch: func(_ context.Context, url string) (int, bool, error) {
			if r.blind {
				return 0, false, errors.New("core not answering")
			}
			if ms, ok := r.answer[url]; ok {
				return ms, true, nil
			}
			return 0, true, errors.New("timeout")
		},
		Logf: func(f string, a ...any) { r.lines = append(r.lines, fmt.Sprintf(f, a...)) },
	})
	r.p.now = func() time.Time { return r.clock }
	return r
}

func (r *rig) look() { r.p.Look(context.Background()); r.clock = r.clock.Add(time.Minute) }

func (r *rig) values() string {
	r.tester = false
	r.p.Look(context.Background())
	var out []string
	for _, l := range r.lines {
		_, v, _ := strings.Cut(l, "ms=")
		out = append(out, v)
	}
	return strings.Join(out, "|")
}

func TestFastestAnswerCounts(t *testing.T) {
	r := newRig()
	r.look()
	delete(r.answer, Sites[0])
	r.look()
	r.answer = map[string]int{}
	r.look()
	r.blind = true
	r.look()
	if got := r.values(); got != "31,44,x,-" {
		t.Fatalf("values %q", got)
	}
	if !strings.HasPrefix(r.lines[0], fmt.Sprintf("[rureach] minutes from=%d ", r.clock.Add(-4*time.Minute).Unix())) {
		t.Fatalf("line %q", r.lines[0])
	}
}

func TestOnlyTesters(t *testing.T) {
	r := newRig()
	r.tester = false
	r.look()
	if got := r.values(); got != "" {
		t.Fatalf("an ordinary user was measured: %q", got)
	}
}

func TestGapsAndFlush(t *testing.T) {
	r := newRig()
	r.look()
	r.clock = r.clock.Add(2 * time.Minute)
	r.look()
	r.clock = r.clock.Add(time.Hour)
	r.look()
	for i := 0; i < 11; i++ {
		r.look()
	}
	if got := r.values(); got != "31,-,-,31|31,31,31,31,31,31,31,31,31,31,31|31" {
		t.Fatalf("values %q", got)
	}
}
