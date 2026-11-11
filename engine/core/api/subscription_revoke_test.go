package api

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type hubTransport func(*http.Request) (*http.Response, error)

func (f hubTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// sealedHub is our hub's sealed body (hikkari-control internal/subscription,
// envelope v2), sealed under token the way the hub does.
func sealedHub(t *testing.T, token string, body any) []byte {
	t.Helper()
	plain, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	const aad = "hikkari-subscription-v2"
	key := sha256.Sum256([]byte(aad + "\x00" + token))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, aead.NonceSize())
	out, err := json.Marshal(map[string]any{"version": 2, "nonce": nonce, "ciphertext": aead.Seal(nil, nonce, plain, []byte(aad))})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// A tester's link from our hub turns tester mode on when it is added; when
// the hub revokes it for good, the subscription and its profiles go, the
// tunnel moves to what is left, and another provider's subscription stays.
func TestATestersLinkTurnsTesterOnAndARevokeDeletesIt(t *testing.T) {
	c, _ := initSyncCore(t)
	const token = "tok-0123456789abcdef"
	const hubURL = "https://sub.hikkari.net/sub/" + token
	var revoked atomic.Bool
	old := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = old })
	http.DefaultTransport = hubTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "sub.hikkari.net" {
			return old.RoundTrip(r)
		}
		if revoked.Load() {
			return &http.Response{StatusCode: http.StatusGone, Header: http.Header{"Hikkari-Subscription": []string{"revoked"}},
				Body: io.NopCloser(strings.NewReader("subscription revoked")), Request: r}, nil
		}
		body := sealedHub(t, token, map[string]any{
			"version": 2, "tester": true, "metrics": []any{},
			"links": []string{"vless://key@h1.test:443#H1", "vless://key@h2.test:443#H2"},
		})
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/vnd.hikkari.subscription+json"}},
			Body: io.NopCloser(bytes.NewReader(body)), Request: r}, nil
	})
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "vless://key@o.test:443#O")
	}))
	defer other.Close()

	if _, err := c.AddProfileFromURI(other.URL); err != nil {
		t.Fatal(err)
	}
	if c.presets.Snapshot().Tester {
		t.Fatal("another provider's link turned tester mode on")
	}
	first, err := c.AddProfileFromURI(hubURL)
	if err != nil {
		t.Fatal(err)
	}
	if !c.presets.Snapshot().Tester {
		t.Fatal("a tester's link left tester mode off")
	}
	if err := c.SwitchProfile(first); err != nil {
		t.Fatal(err)
	}
	if err := c.StartVPN(); err != nil {
		t.Fatal(err)
	}

	revoked.Store(true)
	// Our hub's sources wait out a long interval while the tunnel's health is
	// unknown (subscriptionRefreshDue); the test asks now.
	c.operationMu.Lock()
	pf, err := c.profiles.Read()
	if err == nil {
		for i := range pf.Subscriptions {
			pf.Subscriptions[i].LastAttempt = 0
		}
		err = c.profiles.Write(pf)
	}
	c.operationMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	forceSync(t, c)
	pf, err = c.profiles.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(pf.Subscriptions) != 1 || pf.Subscriptions[0].URL != other.URL {
		t.Fatalf("subscriptions after the revoke: %+v", pf.Subscriptions)
	}
	if len(pf.Profiles) != 1 || pf.Profiles[0].Label != "O" || pf.Active != pf.Profiles[0].Name {
		t.Fatalf("profiles after the revoke: %+v active %q", pf.Profiles, pf.Active)
	}
	if !c.IsRunning() {
		t.Fatal("the tunnel went down though another profile is left")
	}
	if strings.Contains(c.LogTail(), token) {
		t.Fatal("logs leaked the token")
	}
	// A revoke does not turn tester mode off: the taps own that.
	if !c.presets.Snapshot().Tester {
		t.Fatal("the revoke turned tester mode off")
	}
}
