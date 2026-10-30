package singbox

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const subscriptionServers = "vless://11111111-2222-3333-4444-555555555555@fi.example:443?security=tls&type=xhttp&path=%2Fapi#%F0%9F%87%AB%F0%9F%87%AE%20Finland\r\ntrojan://secret@de.example:443#Germany\r\nss://aes-256-gcm:secret@nl.example:8388#Netherlands"

func TestSubscriptionFormats(t *testing.T) {
	for name, body := range map[string]string{
		"plain":     subscriptionServers,
		"base64":    base64.StdEncoding.EncodeToString([]byte(subscriptionServers)),
		"base64url": base64.RawURLEncoding.EncodeToString([]byte(subscriptionServers)),
		"wrapped-base64": func() string {
			s := base64.StdEncoding.EncodeToString([]byte(subscriptionServers))
			return "\ufeff" + s[:80] + "\r\n" + s[80:] + "\r\n"
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, body)
			}))
			defer server.Close()
			for _, input := range []string{server.URL, `"` + server.URL + `"`, "[subscription](" + server.URL + ")", "<" + server.URL + ">"} {
				drafts, err := ParseURIs(input)
				if err != nil {
					t.Fatal(err)
				}
				if len(drafts) != 3 {
					t.Fatalf("got %d servers, want 3", len(drafts))
				}
				if drafts[0].Label != "🇫🇮 Finland" || drafts[1].Label != "Germany" || drafts[2].Label != "Netherlands" {
					t.Fatalf("labels lost: %+v", drafts)
				}
			}
		})
	}
}

func TestMultipleSubscriptionsExpandAllServers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, base64.StdEncoding.EncodeToString([]byte(subscriptionServers)))
	}))
	defer server.Close()
	drafts, err := ParseURIs("[first](" + server.URL + "/first)\n\"" + server.URL + "/second\"\nvless://id@single.example:443#Single")
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 7 {
		t.Fatalf("got %d servers, want all 7", len(drafts))
	}
	if drafts[3].Label != "🇫🇮 Finland" || drafts[6].Label != "Single" {
		t.Fatalf("wrong order: %+v", drafts)
	}
}

func TestSubscriptionErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		status     int
		body, want string
	}{
		"http":      {http.StatusForbidden, "forbidden", "HTTP 403"},
		"empty":     {http.StatusOK, "", "пустая"},
		"html":      {http.StatusOK, "<html>subscription page</html>", "разобрать"},
		"nested":    {http.StatusOK, "https://example.com/recursive", "другую HTTP-ссылку"},
		"oversized": {http.StatusOK, strings.Repeat("x", 2*1024*1024+1), "2 МиБ"},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			_, err := ParseURIs(server.URL)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}

func TestSubscriptionDeviceIdentity(t *testing.T) {
	const deviceID = "0123456789abcdef0123456789abcdef"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Hwid") != deviceID || r.Header.Get("X-Device-Os") == "" {
			t.Error("missing device identity headers")
		}
		_, _ = io.WriteString(w, subscriptionServers)
	}))
	defer server.Close()
	for i := 0; i < 2; i++ {
		drafts, err := ParseURIsForDevice(server.URL, deviceID)
		if err != nil || len(drafts) != 3 {
			t.Fatalf("servers=%d err=%v", len(drafts), err)
		}
	}
}

func TestProviderErrorsAreNotProfiles(t *testing.T) {
	for _, label := range []string{"Клиент не поддерживаеться", "Неподдерживаемый клиент", "Unsupported client", "Client not supported"} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, base64.StdEncoding.EncodeToString([]byte("vless://id@127.0.0.1:443#"+url.PathEscape(label))))
		}))
		drafts, err := ParseURIs(server.URL)
		server.Close()
		if !errors.Is(err, ErrUnsupportedClient) || len(drafts) != 0 {
			t.Fatalf("label=%s count=%d err=%v", label, len(drafts), err)
		}
	}
	for _, tc := range []struct{ header, want string }{
		{"X-Hwid-Not-Supported", "идентификатор"},
		{"X-Hwid-Max-Devices-Reached", "лимит устройств"},
		{"X-Hwid-Limit", "лимит устройств"},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set(tc.header, "true")
			_, _ = io.WriteString(w, subscriptionServers)
		}))
		drafts, err := ParseURIs(server.URL)
		server.Close()
		if err == nil || !strings.Contains(err.Error(), tc.want) || len(drafts) != 0 {
			t.Fatalf("%s count=%d err=%v", tc.header, len(drafts), err)
		}
	}
}
