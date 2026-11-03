package amnezia

import (
	"context"
	"crypto/aes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testAPIKey = "test-account-key"

func premiumKey(t *testing.T) string {
	t.Helper()
	link, err := encodeLink([]byte(`{"name":"Amnezia Premium","config_version":2,` +
		`"api_config":{"service_type":"amnezia-premium","service_protocol":"awg","user_country_code":"ru"},` +
		`"auth_data":{"api_key":"` + testAPIKey + `"}}`))
	if err != nil {
		t.Fatal(err)
	}
	return link
}

// fakeGateway follows the wire protocol: it opens the envelope with its private
// key and seals the answer with the session key found inside.
type fakeGateway struct {
	t          *testing.T
	privateKey *rsa.PrivateKey
	answer     func(request map[string]any) string
	requests   []map[string]any
}

func (g *fakeGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var envelope struct {
		KeyPayload []byte `json:"key_payload"`
		APIPayload []byte `json:"api_payload"`
	}
	data, _ := io.ReadAll(r.Body)
	if err := json.Unmarshal(data, &envelope); err != nil {
		g.t.Errorf("envelope: %v", err)
		return
	}
	opened, err := rsa.DecryptPKCS1v15(nil, g.privateKey, envelope.KeyPayload)
	if err != nil {
		g.t.Errorf("session keys: %v", err)
		return
	}
	var session struct {
		Key  []byte `json:"aes_key"`
		IV   []byte `json:"aes_iv"`
		Salt []byte `json:"aes_salt"`
	}
	if err := json.Unmarshal(opened, &session); err != nil {
		g.t.Errorf("session json: %v", err)
		return
	}
	if len(session.Key) != 32 || len(session.IV) != 32 || len(session.Salt) != 8 {
		g.t.Errorf("session sizes: %d %d %d", len(session.Key), len(session.IV), len(session.Salt))
	}
	plain, err := decryptCBC(envelope.APIPayload, session.Key, session.IV[:aes.BlockSize])
	if err != nil {
		g.t.Errorf("payload: %v", err)
		return
	}
	var request map[string]any
	if err := json.Unmarshal(plain, &request); err != nil {
		g.t.Errorf("payload json: %v", err)
		return
	}
	g.requests = append(g.requests, request)
	sealed, err := encryptCBC([]byte(g.answer(request)), session.Key, session.IV[:aes.BlockSize])
	if err != nil {
		g.t.Errorf("answer: %v", err)
		return
	}
	w.Write(sealed)
}

func newGateway(t *testing.T, answer func(map[string]any) string) (*fakeGateway, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return &fakeGateway{t: t, privateKey: key, answer: answer},
		pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}

func configAnswer(t *testing.T) string {
	t.Helper()
	inner, _ := json.Marshal(map[string]string{
		"config": "[Interface]\nPrivateKey = " + privateKeyPlaceholder + "\n",
	})
	document, _ := json.Marshal(map[string]any{
		"containers": []any{map[string]any{"awg": map[string]string{"last_config": string(inner)}, "container": "amnezia-awg"}},
	})
	link, err := encodeLink(document)
	if err != nil {
		t.Fatal(err)
	}
	answer, _ := json.Marshal(map[string]string{"config": link})
	return string(answer)
}

func TestParseServiceKey(t *testing.T) {
	key, ok := ParseServiceKey(premiumKey(t))
	if !ok || key.APIKey != testAPIKey || key.ServiceType != "amnezia-premium" || key.UserCountry != "ru" {
		t.Fatalf("got %+v, %v", key, ok)
	}
	server, _ := encodeLink([]byte(`{"containers":[{"container":"amnezia-awg"}],"auth_data":{"api_key":"x"},"api_config":{"service_type":"y"}}`))
	for name, link := range map[string]string{
		"server link":  server,
		"other scheme": "vless://id@host:443",
		"garbage":      "vpn://%%%",
		"empty":        "vpn://",
	} {
		if _, ok := ParseServiceKey(link); ok {
			t.Errorf("%s: taken for a service key", name)
		}
	}
}

func TestFetchConfigPutsDeviceKeyIntoConfig(t *testing.T) {
	gateway, publicKey := newGateway(t, func(map[string]any) string { return configAnswer(t) })
	server := httptest.NewServer(gateway)
	defer server.Close()
	client := &Client{Endpoint: server.URL, PublicKey: publicKey, HTTP: server.Client()}
	key, _ := ParseServiceKey(premiumKey(t))

	link, err := client.FetchConfig(context.Background(), key, "device-a")
	if err != nil {
		t.Fatal(err)
	}
	document, err := decodeLink(link)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(document), privateKeyPlaceholder) {
		t.Fatal("placeholder left in config")
	}
	if strings.Contains(string(document), testAPIKey) {
		t.Fatal("account key leaked into the server link")
	}

	request := gateway.requests[0]
	auth, _ := request["auth_data"].(map[string]any)
	if auth["api_key"] != testAPIKey || request["service_type"] != "amnezia-premium" || request["service_protocol"] != "awg" {
		t.Fatalf("request: %v", request)
	}
	rawPublic, err := base64.StdEncoding.DecodeString(request["public_key"].(string))
	if err != nil || len(rawPublic) != 32 {
		t.Fatalf("public key: %v", request["public_key"])
	}

	if _, err := client.FetchConfig(context.Background(), key, "device-a"); err != nil {
		t.Fatal(err)
	}
	if gateway.requests[0]["installation_uuid"] != gateway.requests[1]["installation_uuid"] {
		t.Fatal("one device must keep one installation_uuid, or it takes another slot")
	}
	if gateway.requests[0]["public_key"] == gateway.requests[1]["public_key"] {
		t.Fatal("each config needs its own key pair")
	}
	if strings.Contains(gateway.requests[0]["installation_uuid"].(string), "device-a") {
		t.Fatal("our device id must not reach the gateway")
	}
}

func TestFetchConfigExplainsRefusalsWithoutTheKey(t *testing.T) {
	for status, want := range map[int]string{409: "лимит устройств", 402: "не активна", 429: "много запросов", 500: "код 500"} {
		answer, _ := json.Marshal(map[string]any{"http_status": status, "message": "details"})
		gateway, publicKey := newGateway(t, func(map[string]any) string { return string(answer) })
		server := httptest.NewServer(gateway)
		client := &Client{Endpoint: server.URL, PublicKey: publicKey, HTTP: server.Client()}
		key, _ := ParseServiceKey(premiumKey(t))
		_, err := client.FetchConfig(context.Background(), key, "device-a")
		server.Close()
		if err == nil || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), testAPIKey) {
			t.Errorf("%d: %v", status, err)
		}
	}
}

func TestFetchConfigFallsBackToPublishedProxy(t *testing.T) {
	gateway, publicKey := newGateway(t, func(map[string]any) string { return configAnswer(t) })
	proxy := httptest.NewServer(gateway)
	defer proxy.Close()

	// A blocked gateway: a middlebox answers instead of the service.
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("<html>blocked</html>"))
	}))
	defer blocked.Close()

	list, _ := json.Marshal([]string{proxy.URL})
	sum := sha512.Sum512(publicKey)
	sealed, err := encryptCBC(list, sum[:32], sum[32:48])
	if err != nil {
		t.Fatal(err)
	}
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/endpoints.json" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(base64.StdEncoding.EncodeToString(sealed)))
	}))
	defer storage.Close()

	client := &Client{Endpoint: blocked.URL, PublicKey: publicKey, Storages: []string{storage.URL}, HTTP: http.DefaultClient}
	key, _ := ParseServiceKey(premiumKey(t))
	if _, err := client.FetchConfig(context.Background(), key, "device-a"); err != nil {
		t.Fatal(err)
	}
	if len(gateway.requests) != 1 {
		t.Fatalf("proxy got %d requests", len(gateway.requests))
	}
}

func TestFetchConfigFailsWhenEveryPathIsBlocked(t *testing.T) {
	blocked := httptest.NewServer(http.NotFoundHandler())
	defer blocked.Close()
	_, publicKey := newGateway(t, nil)
	client := &Client{Endpoint: blocked.URL, PublicKey: publicKey, Storages: []string{blocked.URL}, HTTP: http.DefaultClient}
	key, _ := ParseServiceKey(premiumKey(t))
	_, err := client.FetchConfig(context.Background(), key, "device-a")
	if err == nil || !strings.Contains(err.Error(), "недоступен") {
		t.Fatalf("got %v", err)
	}
}

func TestShippedKeyIsExactBytes(t *testing.T) {
	if strings.HasSuffix(string(gatewayPublicKey), "\n") {
		t.Fatal("the proxy-list key is derived from these bytes: no trailing newline")
	}
	block, _ := pem.Decode(gatewayPublicKey)
	if block == nil {
		t.Fatal("shipped key is not PEM")
	}
	if _, err := x509.ParsePKIXPublicKey(block.Bytes); err != nil {
		t.Fatal(err)
	}
}
