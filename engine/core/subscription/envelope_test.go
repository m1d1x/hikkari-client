package subscription

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestSealedSubscriptionIntegrity(t *testing.T) {
	token := "test-192-bit-subscription-token"
	body := []byte(`{"version":2,"links":["vless://secret@example.test"],"metrics":[]}`)
	a, err := sealSubscription(token, body)
	if err != nil {
		t.Fatal(err)
	}
	b, err := sealSubscription(token, body)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) || bytes.Contains(a, []byte("vless")) {
		t.Fatal("nonce reuse or plaintext leak")
	}
	opened, err := openSubscription(token, a)
	if err != nil || !bytes.Equal(opened, body) {
		t.Fatal("roundtrip", err)
	}
	if _, err := openSubscription("other-token", a); err == nil {
		t.Fatal("wrong credential accepted")
	}
	var sealed sealedSubscription
	if err := json.Unmarshal(a, &sealed); err != nil {
		t.Fatal(err)
	}
	sealed.Ciphertext[0] ^= 1
	bad, _ := json.Marshal(sealed)
	if _, err := openSubscription(token, bad); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
	sealed.Nonce = nil
	bad, _ = json.Marshal(sealed)
	if _, err := openSubscription(token, bad); err == nil {
		t.Fatal("malformed nonce accepted")
	}
}
