// Package amnezia trades an Amnezia Premium key for a server config at Amnezia's
// gateway. Such a key names no server: it holds an account key the gateway
// answers with an AmneziaWG config, built for a public key generated here.
package amnezia

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"
)

// gatewayPublicKey ships byte for byte, with no trailing newline: the key that
// opens the proxy lists is derived from these exact bytes. Amnezia injects it
// into its builds from a CI secret, so it is not in their source repository.
//
//go:embed gateway-public.pem
var gatewayPublicKey []byte

const (
	gatewayEndpoint = "http://gw.amnezia.org:80/"

	// The gateway refuses clients it considers outdated, so requests carry the
	// values of the Amnezia release this exchange was written against.
	clientName    = "AmneziaVPN"
	clientVersion = "5.0.3.0"

	privateKeyPlaceholder = "$WIREGUARD_CLIENT_PRIVATE_KEY"

	maxDocumentBytes = 1 << 20
	maxResponseBytes = 4 << 20
	maxProxyAttempts = 5

	fetchTimeout   = 60 * time.Second
	requestTimeout = 12 * time.Second
	storageTimeout = 3 * time.Second
)

var (
	storageEndpoints = []string{
		"https://s3.eu-north-1.amazonaws.com/amnezia/",
		"https://storage.googleapis.com/lambda-list/",
		"https://amnzstrg01.blob.core.windows.net/lambda-list/",
		"https://objectstorage.eu-zurich-1.oraclecloud.com/n/zrhfyaq6qxvh/b/lambda-list/o/",
		"https://storage.mwsapis.ru/lambda-list/",
	}
	// Answers that are the service's own verdict even with a 404 status. Any
	// other 404 can come from whatever is blocking the gateway.
	genuineNotFound = []string{
		"No active configuration found for",
		"No non-revoked public key found for",
		"Account not found.",
	}
)

// ServiceKey is the account behind an Amnezia Premium vpn:// key.
type ServiceKey struct {
	APIKey      string
	ServiceType string
	UserCountry string
}

// ParseServiceKey recognises a vpn:// link that holds an account key rather
// than a server. Every other link is left to the protocol decoder.
func ParseServiceKey(link string) (ServiceKey, bool) {
	document, err := decodeLink(link)
	if err != nil {
		return ServiceKey{}, false
	}
	var parsed struct {
		APIConfig struct {
			ServiceType string `json:"service_type"`
			UserCountry string `json:"user_country_code"`
		} `json:"api_config"`
		AuthData struct {
			APIKey string `json:"api_key"`
		} `json:"auth_data"`
		Containers json.RawMessage `json:"containers"`
	}
	if err := json.Unmarshal(document, &parsed); err != nil {
		return ServiceKey{}, false
	}
	if parsed.AuthData.APIKey == "" || parsed.APIConfig.ServiceType == "" || len(parsed.Containers) > 0 {
		return ServiceKey{}, false
	}
	return ServiceKey{
		APIKey:      parsed.AuthData.APIKey,
		ServiceType: parsed.APIConfig.ServiceType,
		UserCountry: parsed.APIConfig.UserCountry,
	}, true
}

func decodeLink(link string) ([]byte, error) {
	link = strings.TrimSpace(link)
	if len(link) < 6 || !strings.EqualFold(link[:6], "vpn://") {
		return nil, errors.New("not a vpn link")
	}
	encoded := link[6:]
	if i := strings.IndexAny(encoded, "?#"); i >= 0 {
		encoded = encoded[:i]
	}
	compressed, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(encoded, "=/"))
	if err != nil {
		return nil, err
	}
	return inflate(compressed)
}

// inflate undoes Qt's qCompress: a four-byte size followed by a zlib stream.
func inflate(data []byte) ([]byte, error) {
	if len(data) < 4 {
		return nil, errors.New("short document")
	}
	reader, err := zlib.NewReader(bytes.NewReader(data[4:]))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	out, err := io.ReadAll(io.LimitReader(reader, maxDocumentBytes+1))
	if err != nil {
		return nil, err
	}
	if len(out) > maxDocumentBytes {
		return nil, errors.New("document too large")
	}
	return out, nil
}

func encodeLink(document []byte) (string, error) {
	var compressed bytes.Buffer
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(document)))
	compressed.Write(size[:])
	writer := zlib.NewWriter(&compressed)
	if _, err := writer.Write(document); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	return "vpn://" + base64.RawURLEncoding.EncodeToString(compressed.Bytes()), nil
}

// Client talks to one gateway. The zero value is not usable, see Default.
type Client struct {
	Endpoint  string
	PublicKey []byte
	Storages  []string
	HTTP      *http.Client
}

// Default is the production gateway.
var Default = &Client{
	Endpoint:  gatewayEndpoint,
	PublicKey: gatewayPublicKey,
	Storages:  storageEndpoints,
	HTTP: &http.Client{
		// A redirect would resend the envelope to a host nobody chose.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	},
}

// FetchConfig asks the gateway for this installation's config and returns it as
// a vpn:// server link. deviceID must be stable: Amnezia counts one device per
// installation, so a new identity would take another slot of the subscription.
func (c *Client) FetchConfig(ctx context.Context, key ServiceKey, deviceID string) (string, error) {
	if deviceID == "" {
		return "", errors.New("amnezia: нет идентификатора устройства")
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	privateKey, publicKey, err := newWireGuardKeys()
	if err != nil {
		return "", fmt.Errorf("amnezia: создание ключей: %w", err)
	}
	payload, err := json.Marshal(map[string]any{
		"os_version":        productType(),
		"app_version":       clientVersion,
		"cli_name":          clientName,
		"app_language":      "ru",
		"installation_uuid": installationUUID(deviceID),
		"user_country_code": key.UserCountry,
		"service_type":      key.ServiceType,
		"service_protocol":  "awg",
		"public_key":        publicKey,
		"auth_data":         map[string]string{"api_key": key.APIKey},
	})
	if err != nil {
		return "", err
	}
	body, err := c.post(ctx, "v1/config", payload, key)
	if err != nil {
		return "", err
	}
	return buildServerLink(body, privateKey)
}

// buildServerLink puts the private key, which never left this device, into the
// config the gateway answered with.
func buildServerLink(body []byte, privateKey string) (string, error) {
	if err := responseError(body); err != nil {
		return "", err
	}
	var response struct {
		Config string `json:"config"`
	}
	if err := json.Unmarshal(body, &response); err != nil || response.Config == "" {
		return "", errors.New("amnezia: шлюз не прислал конфиг")
	}
	document, err := decodeLink(response.Config)
	if err != nil {
		return "", errors.New("amnezia: шлюз прислал неразборчивый конфиг")
	}
	if !bytes.Contains(document, []byte(privateKeyPlaceholder)) {
		return "", errors.New("amnezia: в конфиге шлюза нет места для ключа устройства")
	}
	return encodeLink(bytes.ReplaceAll(document, []byte(privateKeyPlaceholder), []byte(privateKey)))
}

func responseError(body []byte) error {
	status, message := apiStatus(body)
	switch {
	case status < 0 || status < 300:
		return nil
	case status == 409:
		return errors.New("amnezia: достигнут лимит устройств подписки, освободите устройство в приложении Amnezia")
	case status == 402 || status == 422:
		return errors.New("amnezia: подписка Premium не активна или закончилась")
	case status == 404:
		return errors.New("amnezia: шлюз не нашёл подписку этого ключа")
	case status == 429:
		return errors.New("amnezia: слишком много запросов к шлюзу, повторите позже")
	case status == 501 && strings.Contains(message, "update is required"):
		return errors.New("amnezia: шлюз требует более новую версию клиента, обновите приложение")
	}
	return fmt.Errorf("amnezia: шлюз отказал (код %d)", status)
}

func apiStatus(body []byte) (int, string) {
	var parsed struct {
		Status  *int   `json:"http_status"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.Status == nil {
		return -1, ""
	}
	return *parsed.Status, parsed.Message
}

// post tries the gateway directly, then through Amnezia's proxies, whose list it
// publishes encrypted on several storages for networks that block the gateway.
func (c *Client) post(ctx context.Context, path string, payload []byte, key ServiceKey) ([]byte, error) {
	envelope, aesKey, iv, err := c.seal(payload)
	if err != nil {
		return nil, err
	}
	body, trusted := c.attempt(ctx, c.Endpoint, path, envelope, aesKey, iv)
	if trusted {
		return body, nil
	}
	proxies := c.proxies(ctx, key)
	if len(proxies) > maxProxyAttempts {
		proxies = proxies[:maxProxyAttempts]
	}
	for _, proxy := range proxies {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if body, trusted = c.attempt(ctx, proxy, path, envelope, aesKey, iv); trusted {
			return body, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, errors.New("amnezia: шлюз недоступен из этой сети")
}

// seal builds the request envelope. The gateway expects two oddities: the IV is
// sent as 32 bytes while CBC uses the first 16, and an unused salt comes along.
func (c *Client) seal(payload []byte) (envelope, aesKey, iv []byte, err error) {
	block, _ := pem.Decode(c.PublicKey)
	if block == nil {
		return nil, nil, nil, errors.New("amnezia: повреждён ключ шлюза")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("amnezia: повреждён ключ шлюза: %w", err)
	}
	rsaKey, ok := parsed.(*rsa.PublicKey)
	if !ok {
		return nil, nil, nil, errors.New("amnezia: повреждён ключ шлюза")
	}
	aesKey, iv = make([]byte, 32), make([]byte, 32)
	salt := make([]byte, 8)
	for _, b := range [][]byte{aesKey, iv, salt} {
		if _, err := rand.Read(b); err != nil {
			return nil, nil, nil, err
		}
	}
	sessionKeys, err := json.Marshal(map[string][]byte{"aes_key": aesKey, "aes_iv": iv, "aes_salt": salt})
	if err != nil {
		return nil, nil, nil, err
	}
	wrapped, err := rsa.EncryptPKCS1v15(rand.Reader, rsaKey, sessionKeys)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("amnezia: шифрование запроса: %w", err)
	}
	sealed, err := encryptCBC(payload, aesKey, iv[:aes.BlockSize])
	if err != nil {
		return nil, nil, nil, err
	}
	envelope, err = json.Marshal(map[string][]byte{"key_payload": wrapped, "api_payload": sealed})
	return envelope, aesKey, iv, err
}

// attempt reports whether the answer really came from the service. Anything
// else, a middlebox's page included, sends the request on to the next path.
func (c *Client) attempt(ctx context.Context, base, path string, envelope, aesKey, iv []byte) ([]byte, bool) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, joinURL(base, path), bytes.NewReader(envelope))
	if err != nil {
		return nil, false
	}
	request.Header.Set("Content-Type", "application/json")
	sealed, ok := c.read(request)
	if !ok {
		return nil, false
	}
	body, err := decryptCBC(sealed, aesKey, iv[:aes.BlockSize])
	if err != nil || !answeredByService(body) {
		return nil, false
	}
	return body, true
}

func (c *Client) read(request *http.Request) ([]byte, bool) {
	response, err := c.HTTP.Do(request)
	if err != nil {
		return nil, false
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(data) > maxResponseBytes {
		return nil, false
	}
	return data, true
}

func answeredByService(body []byte) bool {
	if bytes.Contains(body, []byte("html")) {
		return false
	}
	status, message := apiStatus(body)
	switch status {
	case 404:
		for _, known := range genuineNotFound {
			if strings.Contains(message, known) {
				return true
			}
		}
		return false
	case 501:
		return strings.Contains(message, "update is required")
	case 422:
		return strings.Contains(message, "Is it activated?")
	}
	return true
}

func (c *Client) proxies(ctx context.Context, key ServiceKey) []string {
	token := base64.RawURLEncoding.EncodeToString([]byte("endpoints-" + key.ServiceType + "-" + key.UserCountry))
	for _, name := range []string{token + ".json", "endpoints.json"} {
		for _, storage := range c.Storages {
			if list := c.proxyList(ctx, joinURL(storage, name)); len(list) > 0 {
				return list
			}
			if ctx.Err() != nil {
				return nil
			}
		}
	}
	return nil
}

func (c *Client) proxyList(ctx context.Context, target string) []string {
	ctx, cancel := context.WithTimeout(ctx, storageTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil
	}
	data, ok := c.read(request)
	if !ok {
		return nil
	}
	list, err := decodeProxyList(data, c.PublicKey)
	if err != nil {
		return nil
	}
	return list
}

func decodeProxyList(data, publicKey []byte) ([]string, error) {
	sealed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil {
		return nil, err
	}
	sum := sha512.Sum512(publicKey)
	plain, err := decryptCBC(sealed, sum[:32], sum[32:48])
	if err != nil {
		return nil, err
	}
	var list []string
	if err := json.Unmarshal(plain, &list); err != nil {
		return nil, err
	}
	return list, nil
}

func encryptCBC(data, key, iv []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	padding := aes.BlockSize - len(data)%aes.BlockSize
	sealed := append(append([]byte{}, data...), bytes.Repeat([]byte{byte(padding)}, padding)...)
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(sealed, sealed)
	return sealed, nil
}

func decryptCBC(data, key, iv []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data)%aes.BlockSize != 0 {
		return nil, errors.New("bad ciphertext length")
	}
	plain := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, data)
	padding := int(plain[len(plain)-1])
	if padding == 0 || padding > aes.BlockSize {
		return nil, errors.New("bad padding")
	}
	return plain[:len(plain)-padding], nil
}

func newWireGuardKeys() (privateKey, publicKey string, err error) {
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(key.Bytes()),
		base64.StdEncoding.EncodeToString(key.PublicKey().Bytes()), nil
}

// installationUUID keeps our own device ID away from a third party while staying
// stable, so re-importing a key does not take another device slot.
func installationUUID(deviceID string) string {
	sum := sha256.Sum256([]byte("amnezia-installation:" + deviceID))
	sum[6] = sum[6]&0x0f | 0x40
	sum[8] = sum[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

func productType() string {
	if runtime.GOOS == "darwin" {
		return "macos"
	}
	return runtime.GOOS
}

func joinURL(base, path string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
}
