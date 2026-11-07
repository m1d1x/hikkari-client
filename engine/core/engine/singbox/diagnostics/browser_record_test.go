package diag

import (
	"net"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

// TestRecordBrowserHello writes the ClientHello a real browser sends to
// testdata/hello/browser-<name>.bin: set HIKKARI_BROWSER_RECORD=<name> (for
// example chrome-142) and open https://127.0.0.1:<HIKKARI_BROWSER_PORT, 47443>/
// in the browser within two minutes. The handshake is never finished: the
// browser shows a connection error, and its hello is what the fingerprints of
// the core are held to (plan knob-truth W4, TestCoreFingerprintsMatchBrowsers).
func TestRecordBrowserHello(t *testing.T) {
	name := os.Getenv("HIKKARI_BROWSER_RECORD")
	if name == "" {
		t.Skip("HIKKARI_BROWSER_RECORD is not set")
	}
	if !regexp.MustCompile(`^[a-z]+-[0-9]+(\.[0-9]+)*[a-z0-9-]*$`).MatchString(name) {
		t.Fatalf("name %q: browser-version, as chrome-142", name)
	}
	port := os.Getenv("HIKKARI_BROWSER_PORT")
	if port == "" {
		port = "47443"
	}
	l, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	_ = l.(*net.TCPListener).SetDeadline(time.Now().Add(2 * time.Minute))
	conn, err := l.Accept()
	if err != nil {
		t.Fatalf("no browser came: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(wireWait))
	msg, records, err := readClientHello(conn)
	if err != nil {
		t.Fatal(err)
	}
	h, err := parseClientHello(msg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("testdata", "hello", "browser-"+name+".bin"), msg, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("%s: %d records, %s", name, records, h.fields())
}
