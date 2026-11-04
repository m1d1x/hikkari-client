package diag

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	config "NekkoVPN/core/engine/singbox/config"
)

// The Russian zone's thousands of addresses must not push the rules after them
// off the 8 KB log line.
func TestTheLoggedConfigFitsItsLine(t *testing.T) {
	cfg := config.DefaultConfig()
	config.ApplyRouting(cfg, config.RoutingState{Enabled: false, Mode: config.ModeBypass}, config.SelectorTag)
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var v interface{}
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(abridge(v))
	if len(out) > 8<<10 {
		t.Fatalf("logged config is %d bytes, the line holds 8 KB", len(out))
	}
	short := abridge([]interface{}{"a", "b"}).([]interface{})
	if len(short) != 2 {
		t.Fatalf("a short list was abridged: %v", short)
	}
}

type flowLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *flowLog) Logf(format string, args ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *flowLog) has(part string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range l.lines {
		if strings.HasPrefix(line, "[diag:conn] ") && strings.Contains(line, part) {
			return true
		}
	}
	return false
}

// A browser's TCP is only counted in the periodic rounds, and a snapshot names
// where it goes and how much came back: that is how a hung Secure DNS shows.
func TestOnlyASnapshotDetailsABrowsersTCP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"connections":[
			{"metadata":{"network":"tcp","host":"chrome.cloudflare-dns.com","processPath":"C:\\\\chrome\\\\chrome.exe"},"upload":2048,"download":0,"chains":["p-a","proxy"]},
			{"metadata":{"network":"tcp","host":"discord.com","process":"Discord.exe"},"chains":["p-a","proxy"]}
		]}`)
	}))
	defer srv.Close()
	base := clashBase
	clashBase = srv.URL
	defer func() { clashBase = base }()

	periodic := &flowLog{}
	New(periodic, "").connections(false)
	if periodic.has("chrome.exe") {
		t.Fatalf("a periodic round detailed a browser: %v", periodic.lines)
	}
	if !periodic.has("discord.com") {
		t.Fatalf("a periodic round lost Discord: %v", periodic.lines)
	}

	snapshot := &flowLog{}
	New(snapshot, "").connections(true)
	if !snapshot.has(`host="chrome.cloudflare-dns.com" proc="chrome.exe"`) || !snapshot.has("up=2048 down=0") {
		t.Fatalf("a snapshot hid a browser's flow: %v", snapshot.lines)
	}
}
