package profile

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
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
