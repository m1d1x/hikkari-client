package logging

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPipeLinesFilesHikkariPeriodsUnderTheirSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	l := New(path)
	defer l.Close()
	r, w := io.Pipe()
	done := make(chan struct{})
	go func() { l.PipeLines("service", r); close(done) }()
	_, _ = io.WriteString(w, "+0300 2026-11-10 07:10:00 WARN outbound/hikkari[p-002]: hikkari period s=600 {\"dup\":{\"fresh\":3}}\n")
	_, _ = io.WriteString(w, "+0300 2026-11-10 07:10:01 WARN dns: something\n")
	_ = w.Close()
	<-done
	raw, _ := os.ReadFile(path)
	text := string(raw)
	if !strings.Contains(text, `[hikkari] +0300 2026-11-10 07:10:00 WARN outbound/hikkari[p-002]: hikkari period s=600 {"dup":{"fresh":3}}`) ||
		!strings.Contains(text, "[service] +0300 2026-11-10 07:10:01 WARN dns: something") {
		t.Fatalf("log:\n%s", text)
	}
}
