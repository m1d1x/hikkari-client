// Package subscription handles provider HTTP, device headers and subscription
// envelopes independently of the selected transport or routing component.
package subscription

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Metadata is the provider-level information attached to all server links in
// one HTTP subscription response.
type Metadata struct {
	Title                 string `json:"title,omitempty"`
	UploadBytes           int64  `json:"uploadBytes,omitempty"`
	DownloadBytes         int64  `json:"downloadBytes,omitempty"`
	TotalBytes            int64  `json:"totalBytes,omitempty"`
	ExpiresAt             int64  `json:"expiresAt,omitempty"`
	UpdateIntervalSeconds int64  `json:"updateIntervalSeconds,omitempty"`
	WebPageURL            string `json:"webPageUrl,omitempty"`
	SupportURL            string `json:"supportUrl,omitempty"`
}

type Imported[T any] struct {
	Items     []T
	Metadata  *Metadata
	SourceURL string
}

// Parse expands provider envelopes before handing individual protocol links to
// the decoder. Decoders never receive provider URLs or installation identity.
func Parse[T any](input, deviceID string, decode func(string) (T, error)) ([]T, error) {
	imports, err := ParseDetailed(input, deviceID, decode)
	if err != nil {
		return nil, err
	}
	var out []T
	for _, item := range imports {
		out = append(out, item.Items...)
	}
	return out, nil
}

// ParseDetailed preserves the provider envelope for each imported HTTP
// subscription. Plain server links have no subscription metadata.
func ParseDetailed[T any](input, deviceID string, decode func(string) (T, error)) ([]Imported[T], error) {
	return ParseDetailedContext(context.Background(), input, deviceID, decode)
}

// ParseDetailedContext cancels HTTP expansion with the owning core lifecycle.
func ParseDetailedContext[T any](ctx context.Context, input, deviceID string, decode func(string) (T, error)) ([]Imported[T], error) {
	return parseImportDetailedContext(ctx, input, true, deviceID, decode)
}

// Some providers encode subscription errors as syntactically valid server
// links. They must never become selectable VPN profiles.
var ErrUnsupportedClient = errors.New("провайдер подписки вернул «неподдерживаемый клиент» вместо серверов; получите совместимую подписку или экспортируйте отдельные ссылки серверов из рабочего клиента")

func providerRejectsClient(label string) bool {
	label = strings.ToLower(strings.TrimSpace(label))
	return strings.Contains(label, "unsupported client") ||
		strings.Contains(label, "client not supported") ||
		(strings.Contains(label, "клиент") &&
			(strings.Contains(label, "не поддерж") || strings.Contains(label, "неподдерж")))
}

// NormalizeLink accepts links copied from chat messages as well as raw
// clipboard text. Only strip balanced wrappers; URL query/fragment data stays
// untouched.
func NormalizeLink(input string) string {
	input = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(input), "\ufeff"))
	if strings.Contains(input, "\n") {
		return input
	}
	for _, pair := range [][2]string{{"\"", "\""}, {"'", "'"}, {"`", "`"}, {"<", ">"}, {"“", "”"}, {"«", "»"}} {
		if strings.HasPrefix(input, pair[0]) && strings.HasSuffix(input, pair[1]) && len(input) >= len(pair[0])+len(pair[1]) {
			input = strings.TrimSpace(input[len(pair[0]) : len(input)-len(pair[1])])
		}
	}
	if strings.HasPrefix(input, "[") && strings.HasSuffix(input, ")") {
		if i := strings.Index(input, "]("); i >= 0 {
			input = strings.TrimSpace(input[i+2 : len(input)-1])
		}
	}
	return input
}

func isSubscriptionURL(input string) bool {
	lower := strings.ToLower(input)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func parseImport[T any](input string, allowSubscriptions bool, deviceID string, decode func(string) (T, error)) ([]T, error) {
	imports, err := parseImportDetailed(input, allowSubscriptions, deviceID, decode)
	if err != nil {
		return nil, err
	}
	var out []T
	for _, item := range imports {
		out = append(out, item.Items...)
	}
	return out, nil
}

func parseImportDetailed[T any](input string, allowSubscriptions bool, deviceID string, decode func(string) (T, error)) ([]Imported[T], error) {
	return parseImportDetailedContext(context.Background(), input, allowSubscriptions, deviceID, decode)
}

func parseImportDetailedContext[T any](ctx context.Context, input string, allowSubscriptions bool, deviceID string, decode func(string) (T, error)) ([]Imported[T], error) {
	input = NormalizeLink(input)
	if input == "" {
		return nil, fmt.Errorf("пустая ссылка")
	}
	if strings.Contains(input, "[Interface]") && strings.Contains(input, "[Peer]") {
		d, err := decode(input)
		if err != nil {
			return nil, err
		}
		return []Imported[T]{{Items: []T{d}}}, nil
	}
	// Providers can wrap base64 over several lines, including CRLF.
	if decoded, err := decodeBase64Subscription(input); err == nil {
		input = decoded
	}

	var imports []Imported[T]
	var draftCount int
	var firstErr error
	for _, line := range strings.Split(input, "\n") {
		line = NormalizeLink(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		if isSubscriptionURL(line) {
			// Expand every subscription in a pasted list. ParseURI only returns
			// one draft, which used to silently discard the remaining servers.
			if !allowSubscriptions {
				return nil, fmt.Errorf("подписка содержит другую HTTP-ссылку вместо серверов")
			}
			servers, err := Fetch(ctx, line, deviceID, decode)
			if err != nil {
				return nil, err
			}
			imports = append(imports, servers)
			draftCount += len(servers.Items)
			continue
		}
		if u, err := url.Parse(line); err == nil && providerRejectsClient(u.Fragment) {
			return nil, ErrUnsupportedClient
		}
		d, err := decode(line)
		if err != nil {
			if errors.Is(err, ErrUnsupportedClient) {
				return nil, err
			}
			if !allowSubscriptions {
				return nil, fmt.Errorf("подписка содержит неподдерживаемую или повреждённую запись: %w", err)
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		imports = append(imports, Imported[T]{Items: []T{d}})
		draftCount++
	}
	if draftCount == 0 && firstErr != nil {
		return nil, firstErr
	}
	if draftCount == 0 {
		return nil, fmt.Errorf("в ссылке не найдено поддерживаемых серверов")
	}
	return imports, nil
}

func fetchSubscriptionForDevice[T any](subURL, deviceID string, decode func(string) (T, error)) ([]T, error) {
	imported, err := fetchSubscriptionForDeviceDetailed(subURL, deviceID, decode)
	if err != nil {
		return nil, err
	}
	return imported.Items, nil
}

func fetchSubscriptionForDeviceDetailed[T any](subURL, deviceID string, decode func(string) (T, error)) (Imported[T], error) {
	return Fetch(context.Background(), subURL, deviceID, decode)
}

// Fetch accepts a complete provider snapshot; partial decoding must never prune servers.
func Fetch[T any](ctx context.Context, subURL, deviceID string, decode func(string) (T, error)) (Imported[T], error) {
	client := &http.Client{
		Timeout: 15 * time.Second,
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, subURL, nil)
	if err != nil {
		return Imported[T]{}, fmt.Errorf("ошибка создания запроса подписки: %w", err)
	}
	req.Header.Set("User-Agent", "v2rayN/6.42 Hikkari/1.0")
	if deviceID != "" {
		req.Header.Set("X-Hwid", deviceID)
		osName := runtime.GOOS
		if osName == "darwin" {
			osName = "macOS"
		}
		req.Header.Set("X-Device-Os", osName)
	}

	resp, err := client.Do(req)
	if err != nil {
		return Imported[T]{}, fmt.Errorf("не удалось загрузить подписку: %w", requestCause(err))
	}
	defer resp.Body.Close()
	if strings.EqualFold(resp.Header.Get("X-Hwid-Not-Supported"), "true") {
		return Imported[T]{}, fmt.Errorf("провайдер подписки требует постоянный идентификатор устройства (HWID)")
	}
	if strings.EqualFold(resp.Header.Get("X-Hwid-Max-Devices-Reached"), "true") ||
		strings.EqualFold(resp.Header.Get("X-Hwid-Limit"), "true") {
		return Imported[T]{}, fmt.Errorf("достигнут лимит устройств подписки; удалите старое устройство в кабинете провайдера")
	}

	if resp.StatusCode != http.StatusOK {
		return Imported[T]{}, &HTTPError{Status: resp.StatusCode, RetryAfter: retryAfter(resp.Header.Get("Retry-After"))}
	}

	const maxSubscriptionBytes = 2 * 1024 * 1024
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSubscriptionBytes+1))
	if err != nil {
		return Imported[T]{}, fmt.Errorf("ошибка чтения подписки: %w", err)
	}
	if len(body) > maxSubscriptionBytes {
		return Imported[T]{}, fmt.Errorf("подписка превышает 2 МиБ")
	}
	// Do not follow URLs inside downloaded content: a subscription must
	// contain server URIs, otherwise providers could form an infinite cycle.
	imports, err := parseImportDetailedContext(ctx, string(body), false, deviceID, decode)
	if err != nil {
		return Imported[T]{}, fmt.Errorf("не удалось разобрать подписку: %w", err)
	}
	var items []T
	for _, imported := range imports {
		items = append(items, imported.Items...)
	}
	meta := metadataFromHeaders(resp.Header)
	if meta == nil {
		meta = &Metadata{}
	}
	return Imported[T]{Items: items, Metadata: meta, SourceURL: subURL}, nil
}

// HTTPError carries provider throttling without retaining the credential URL.
type HTTPError struct {
	Status     int
	RetryAfter time.Duration
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("подписка вернула HTTP %d", e.Status)
}
func retryAfter(value string) time.Duration {
	if seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil && seconds > 0 {
		if seconds > 86400 {
			seconds = 86400
		}
		return time.Duration(seconds) * time.Second
	}
	if deadline, err := http.ParseTime(value); err == nil {
		d := time.Until(deadline)
		if d > 24*time.Hour {
			d = 24 * time.Hour
		}
		if d > 0 {
			return d
		}
	}
	return 0
}

func requestCause(err error) error {
	var e *url.Error
	if errors.As(err, &e) {
		return e.Err
	}
	return err
}

func metadataFromHeaders(header http.Header) *Metadata {
	meta := &Metadata{
		Title:                 subscriptionTitle(header),
		UpdateIntervalSeconds: intervalSeconds(header.Get("Profile-Update-Interval")),
		WebPageURL:            strings.TrimSpace(header.Get("Profile-Web-Page-URL")),
		SupportURL:            strings.TrimSpace(header.Get("Support-URL")),
	}
	userinfo := header.Get("Subscription-Userinfo")
	for _, field := range strings.FieldsFunc(userinfo, func(r rune) bool { return r == ';' || r == ',' }) {
		key, value, ok := strings.Cut(strings.TrimSpace(field), "=")
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil || n < 0 {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "upload":
			meta.UploadBytes = n
		case "download":
			meta.DownloadBytes = n
		case "total":
			meta.TotalBytes = n
		case "expire":
			meta.ExpiresAt = n
		}
	}
	if meta.Title == "" && meta.UploadBytes == 0 && meta.DownloadBytes == 0 && meta.TotalBytes == 0 && meta.ExpiresAt == 0 && meta.UpdateIntervalSeconds == 0 && meta.WebPageURL == "" && meta.SupportURL == "" {
		return nil
	}
	return meta
}

func subscriptionTitle(header http.Header) string {
	if disposition := header.Get("Content-Disposition"); disposition != "" {
		if _, params, err := mime.ParseMediaType(disposition); err == nil && strings.TrimSpace(params["filename"]) != "" {
			return strings.TrimSpace(params["filename"])
		}
	}
	title := strings.TrimSpace(header.Get("Profile-Title"))
	if decoded, ok := strings.CutPrefix(title, "base64:"); ok {
		if data, err := base64.StdEncoding.DecodeString(decoded); err == nil {
			title = string(data)
		}
	}
	if left := strings.LastIndex(title, "["); left >= 0 {
		if right := strings.Index(title[left+1:], "]"); right >= 0 {
			title = title[left+1 : left+1+right]
		}
	}
	return strings.TrimSpace(title)
}

func intervalSeconds(value string) int64 {
	hours := positiveHeaderInt(value)
	if hours > 24*365 {
		hours = 24 * 365
	}
	return hours * 3600
}

func positiveHeaderInt(value string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func decodeBase64Subscription(s string) (string, error) {
	s = strings.TrimSpace(s)
	clean := strings.ReplaceAll(strings.ReplaceAll(s, "\r", ""), "\n", "")
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.RawURLEncoding} {
		data, err := enc.DecodeString(clean)
		if err == nil && len(data) > 0 {
			res := string(data)
			if strings.Contains(res, "://") {
				return res, nil
			}
		}
	}
	return "", fmt.Errorf("not base64")
}
