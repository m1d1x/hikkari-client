package singbox

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"NekkoVPN/core/engine"
	"NekkoVPN/core/engine/singbox/clashapi"
	"NekkoVPN/core/engine/singbox/config"
	"NekkoVPN/core/profile"
	"NekkoVPN/core/routing"
)

// A core restored from cache.db runs the member stored there, not the
// selector's default: the profile chosen while it was down has to be applied
// once its API answers, even if that takes a few polls.
func TestStartAppliesProfileChosenWhileCoreWasDown(t *testing.T) {
	var polls atomic.Int32
	var selected atomic.Value
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			body, _ := io.ReadAll(r.Body)
			selected.Store(r.URL.Path + " " + string(body))
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if polls.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer api.Close()

	b := &Backend{config: configfile.New(filepath.Join(t.TempDir(), "config.json")), control: clashapi.New(api.URL)}
	profiles := []profile.Profile{
		{Name: "dead", Label: "Dead", Kind: profile.KindOutbound, Config: map[string]interface{}{
			"type": "vless", "server": "192.0.2.1", "server_port": float64(443), "uuid": "bf000d23-0000-4000-8000-000000000000",
		}},
		{Name: "003", Label: "003", Kind: profile.KindOutbound, Config: map[string]interface{}{
			"type": "hysteria2", "server": "192.0.2.2", "server_port": float64(443), "password": "x",
		}},
	}
	if err := b.Configure(engine.Configuration{Profiles: profiles, ActiveProfile: "003", Routing: routing.DefaultRoutingState()}); err != nil {
		t.Fatal(err)
	}

	b.selectActive(context.Background())

	got, _ := selected.Load().(string)
	if !strings.HasPrefix(got, "/proxies/proxy ") || !strings.Contains(got, `"p-003"`) {
		t.Fatalf("selector was not moved to the chosen profile: %q", got)
	}
}
