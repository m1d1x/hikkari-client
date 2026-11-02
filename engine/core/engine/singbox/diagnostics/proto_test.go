package diag

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseProtoTargetsRefuses(t *testing.T) {
	for name, params := range map[string]string{
		"empty":   `{"targets":[]}`,
		"label":   `{"targets":[{"label":"$(x)","outbound":{"type":"vless","server":"8.8.8.8"}}]}`,
		"type":    `{"targets":[{"label":"a","outbound":{"type":"direct","server":"8.8.8.8"}}]}`,
		"private": `{"targets":[{"label":"a","outbound":{"type":"vless","server":"10.0.0.1"}}]}`,
		"fake":    `{"targets":[{"label":"a","outbound":{"type":"vless","server":"198.18.0.1"}}]}`,
	} {
		if _, err := parseProtoTargets(json.RawMessage(params)); err == nil {
			t.Errorf("%s: want a refusal", name)
		}
	}
	got, err := parseProtoTargets(json.RawMessage(`{"targets":[{"label":"a","outbound":{"type":"vless","server":"host.example"}}]}`))
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
}

func TestProtoConfigStaysOnLoopbackWithNoTun(t *testing.T) {
	b, err := protoConfig(map[string]interface{}{"type": "vless", "server": "8.8.8.8"}, "Ethernet", 1234)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, `"tun"`) || !strings.Contains(s, `"listen":"127.0.0.1"`) || !strings.Contains(s, `"bind_interface":"Ethernet"`) {
		t.Fatal(s)
	}
}

func TestCountRequestsOnlyTrusts204(t *testing.T) {
	status := http.StatusNoContent
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
	defer srv.Close()
	if n, _ := countRequests(context.Background(), srv.Client(), srv.URL); n != protoTries {
		t.Fatalf("n=%d", n)
	}
	status = http.StatusOK
	if n, med := countRequests(context.Background(), srv.Client(), srv.URL); n != 0 || med != "-" {
		t.Fatalf("captive portal counted: %d %s", n, med)
	}
}

func TestProtoLineHidesTheProfile(t *testing.T) {
	line := protoLine(context.Background(), "/nonexistent/sing-box",
		protoTarget{Label: "a-443", Outbound: map[string]interface{}{"type": "vless", "server": "8.8.8.8", "uuid": "SECRET"}}, "lo")
	if strings.Contains(line, "SECRET") || !strings.Contains(line, "a-443 proto_error=") {
		t.Fatal(line)
	}
}
