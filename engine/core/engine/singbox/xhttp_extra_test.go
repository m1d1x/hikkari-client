package singbox

import (
	"net/url"
	"reflect"
	"testing"

	"NekkoVPN/core/profile"
)

const xhttpExtraBase = "vless://11111111-1111-1111-1111-111111111111@example.com:443?security=tls&sni=example.com&type=xhttp&path=%2Fp&mode=packet-up"

func xhttpTransport(t *testing.T, uri string) map[string]interface{} {
	t.Helper()
	draft, err := ParseURI(uri)
	if err != nil {
		t.Fatalf("ParseURI: %v", err)
	}
	tr, _ := draft.Config["transport"].(map[string]interface{})
	if tr == nil {
		t.Fatalf("no transport in %v", draft.Config)
	}
	return tr
}

func TestXHTTPExtraMapsToCoreOptions(t *testing.T) {
	extra := `{"scMinPostsIntervalMs":20,"scMaxEachPostBytes":{"from":500000,"to":1000000},"xPaddingBytes":"100-1000",` +
		`"noGRPCHeader":true,"sessionPlacement":"cookie","headers":{"X-Test":"1"},` +
		`"xmux":{"maxConnections":{"from":2,"to":4},"cMaxReuseTimes":8,"hKeepAlivePeriod":30},"scMaxBufferedPosts":30}`
	tr := xhttpTransport(t, xhttpExtraBase+"&extra="+url.QueryEscape(extra))

	want := map[string]interface{}{
		"sc_min_posts_interval_ms": "20",
		"sc_max_each_post_bytes":   "500000-1000000",
		"x_padding_bytes":          "100-1000",
		"no_grpc_header":           true,
		"session_placement":        "cookie",
	}
	for key, value := range want {
		if tr[key] != value {
			t.Errorf("%s = %#v, want %#v", key, tr[key], value)
		}
	}
	if _, ok := tr["sc_max_buffered_posts"]; ok {
		t.Error("server-only scMaxBufferedPosts must not reach the client options")
	}
	xmux, _ := tr["xmux"].(map[string]interface{})
	wantXmux := map[string]interface{}{"max_connections": "2-4", "c_max_reuse_times": "8", "h_keep_alive_period": float64(30)}
	if !reflect.DeepEqual(xmux, wantXmux) {
		t.Errorf("xmux = %#v, want %#v", xmux, wantXmux)
	}
	if headers, _ := tr["headers"].(map[string]interface{}); headers["X-Test"] != "1" {
		t.Errorf("headers = %#v", tr["headers"])
	}
}

func TestXHTTPWithoutExtraKeepsDefaults(t *testing.T) {
	for name, uri := range map[string]string{
		"absent":    xhttpExtraBase,
		"malformed": xhttpExtraBase + "&extra=" + url.QueryEscape("{not json"),
	} {
		tr := xhttpTransport(t, uri)
		want := map[string]interface{}{"type": "xhttp", "path": "/p", "host": "example.com", "mode": "packet-up"}
		if !reflect.DeepEqual(tr, want) {
			t.Errorf("%s: transport = %#v, want %#v", name, tr, want)
		}
	}
}

// An import stores ExportURI's output, so whatever export drops never reaches
// the running config: the transport must survive the whole round trip.
func TestXHTTPExtraSurvivesExportRoundTrip(t *testing.T) {
	extra := `{"scMinPostsIntervalMs":"10-30","xmux":{"maxConcurrency":"16-32","hKeepAlivePeriod":45},"headers":{"X-Test":"1"}}`
	draft, err := ParseURI(xhttpExtraBase + "&extra=" + url.QueryEscape(extra))
	if err != nil {
		t.Fatal(err)
	}
	exported, err := ExportURI(profile.Profile{Label: draft.Label, Kind: draft.Kind, Config: draft.Config})
	if err != nil {
		t.Fatal(err)
	}
	if again := xhttpTransport(t, exported); !reflect.DeepEqual(again, draft.Config["transport"]) {
		t.Errorf("round trip changed the transport:\n got %#v\nwant %#v", again, draft.Config["transport"])
	}

	plain, err := ParseURI(xhttpExtraBase)
	if err != nil {
		t.Fatal(err)
	}
	link, err := ExportURI(profile.Profile{Label: plain.Label, Kind: plain.Kind, Config: plain.Config})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Has("extra") {
		t.Errorf("plain link grew an extra: %s", link)
	}
}
