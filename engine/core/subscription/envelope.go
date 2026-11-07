package subscription

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
)

const metricsContentType = "application/vnd.hikkari.subscription+json"
const metricsAAD = "hikkari-subscription-v2"

type sealedSubscription struct {
	Version    int    `json:"version"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

func subscriptionCipher(token string) (cipher.AEAD, error) {
	// Domain-separated derivation from a high-entropy subscription bearer.
	// HTTPS remains mandatory: this envelope does not conceal the hub address.
	key := sha256.Sum256([]byte(metricsAAD + "\x00" + token))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func sealSubscription(token string, body []byte) ([]byte, error) {
	aead, err := subscriptionCipher(token)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return json.Marshal(sealedSubscription{2, nonce, aead.Seal(nil, nonce, body, []byte(metricsAAD))})
}

func openSubscription(token string, body []byte) ([]byte, error) {
	var envelope sealedSubscription
	if len(body) > 2<<20 || json.Unmarshal(body, &envelope) != nil || envelope.Version != 2 {
		return nil, errors.New("invalid subscription envelope")
	}
	aead, err := subscriptionCipher(token)
	if err != nil {
		return nil, err
	}
	if len(envelope.Nonce) != aead.NonceSize() {
		return nil, errors.New("invalid subscription nonce")
	}
	return aead.Open(nil, envelope.Nonce, envelope.Ciphertext, []byte(metricsAAD))
}
