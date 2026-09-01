package cliproxy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/quota"
)

const maxResponseBytes = 4 << 20

// Client reads CLIProxyAPI's local management API. The management key and all
// provider OAuth tokens stay on the host; callers only receive normalized quota
// values.
type Client struct {
	baseURL       string
	managementKey string
	httpClient    *http.Client
	maxConcurrent int
	passiveMaxAge time.Duration
	now           func() time.Time
}

type apiCallRequest struct {
	AuthIndex string            `json:"auth_index"`
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Header    map[string]string `json:"header,omitempty"`
	Data      string            `json:"data,omitempty"`
}

type apiCallResponse struct {
	StatusCode int                 `json:"status_code"`
	Header     map[string][]string `json:"header"`
	Body       string              `json:"body"`
}

func NewClient(baseURL, managementKey string, timeout time.Duration) (*Client, error) {
	trimmedURL := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	parsed, err := url.Parse(trimmedURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid CLIProxyAPI base URL %q", baseURL)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("CLIProxyAPI base URL must use http or https")
	}
	if parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
		return nil, errors.New("plaintext CLIProxyAPI management is allowed only on loopback; use HTTPS for a remote host")
	}
	if parsed.User != nil {
		return nil, errors.New("CLIProxyAPI base URL must not contain user credentials")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("CLIProxyAPI base URL must not contain a query or fragment")
	}
	if strings.TrimSpace(managementKey) == "" {
		return nil, errors.New("CLIProxyAPI management key is empty")
	}
	return &Client{
		baseURL:       trimmedURL,
		managementKey: strings.TrimSpace(managementKey),
		httpClient:    newHTTPClient(timeout),
		maxConcurrent: 4,
		passiveMaxAge: 24 * time.Hour,
		now:           time.Now,
	}, nil
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "localhost" {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

func newHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func (client *Client) SetMaxConcurrency(maxConcurrent int) {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	if maxConcurrent > 32 {
		maxConcurrent = 32
	}
	client.maxConcurrent = maxConcurrent
}

func (client *Client) SetPassiveMaxAge(maxAge time.Duration) {
	if maxAge < 0 {
		maxAge = 0
	}
	client.passiveMaxAge = maxAge
}

func (client *Client) Fetch(ctx context.Context) ([]quota.Account, []string, error) {
	entries, err := client.listAuthFiles(ctx)
	if err != nil {
		return nil, nil, err
	}

	visible := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		provider := providerOf(entry)
		if provider == "" {
			continue
		}
		authKind := authKindOf(entry)
		if authKind != "oauth" {
			continue
		}
		visible = append(visible, entry)
	}

	accounts := make([]quota.Account, len(visible))
	sem := make(chan struct{}, client.maxConcurrent)
	var wg sync.WaitGroup
	for index := range visible {
		index := index
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				accounts[index] = quota.Account{
					RetentionID: opaqueAuthIdentity(stringAt(visible[index], "auth_index")),
					Provider:    providerOf(visible[index]),
					Name:        displayName(visible[index]),
					Status:      "error",
					Stale:       true,
					Windows:     []quota.Window{},
					Error:       "refresh cancelled",
				}
				return
			}
			accounts[index] = client.fetchAccount(ctx, visible[index])
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}

	sort.SliceStable(accounts, func(i, j int) bool {
		if accounts[i].Provider != accounts[j].Provider {
			return providerRank(accounts[i].Provider) < providerRank(accounts[j].Provider)
		}
		return strings.ToLower(accounts[i].Name) < strings.ToLower(accounts[j].Name)
	})
	fetchErrors := make([]string, 0)
	for _, account := range accounts {
		if account.Error != "" {
			fetchErrors = append(fetchErrors, fmt.Sprintf("%s: %s", account.Provider, account.Error))
		} else if account.Warning != "" && quotaProviderSupported(account.Provider) {
			fetchErrors = append(fetchErrors, fmt.Sprintf("%s: %s", account.Provider, account.Warning))
		}
	}
	return accounts, fetchErrors, nil
}

func (client *Client) fetchAccount(ctx context.Context, entry map[string]any) quota.Account {
	rawProvider := rawProviderOf(entry)
	provider := providerOf(entry)
	authIndex := stringAt(entry, "auth_index")
	account := quota.Account{
		RetentionID: opaqueAuthIdentity(authIndex),
		Provider:    provider,
		Name:        displayName(entry),
		Plan:        planOf(entry),
		Status:      "unknown",
		Windows:     []quota.Window{},
	}
	if boolAt(entry, "disabled") || strings.EqualFold(stringAt(entry, "status"), "disabled") {
		account.Status = "disabled"
		return account
	}
	if !quotaProviderSupported(provider) {
		account.Stale = true
		account.Warning = "OAuth is connected, but no verified quota adapter is available for this provider"
		return account
	}

	if authIndex == "" {
		account.Status = "error"
		account.Error = "CLIProxyAPI auth entry has no auth_index"
		return account
	}

	var windows []quota.Window
	var plan string
	var err error
	switch rawProvider {
	case "codex":
		windows, plan, err = client.fetchCodex(ctx, authIndex, entry)
	case "claude", "anthropic":
		windows, plan, err = client.fetchClaude(ctx, authIndex)
	case "gemini", "gemini_cli", "gemini-cli", "gemini-cli-oauth":
		windows, plan, err = client.fetchGemini(ctx, authIndex, entry)
	case "antigravity", "anti-gravity":
		windows, plan, err = client.fetchAntigravity(ctx, authIndex, entry)
	case "kimi":
		windows, plan, err = client.fetchKimi(ctx, authIndex)
	case "xai":
		windows, plan, err = client.fetchXAI(ctx, authIndex, entry)
	default:
		err = errors.New("quota adapter routing failed")
	}

	if plan != "" {
		account.Plan = plan
	}
	if err != nil {
		fallback := passiveWindows(provider, entry, client.now(), client.passiveMaxAge)
		if len(fallback) > 0 {
			account.Windows = fallback
			account.Status = quota.StatusForWindows(fallback)
			account.Stale = true
			account.Warning = "active refresh failed; showing the latest CLIProxyAPI response-header observation"
			return account
		}
		account.Status = "error"
		account.Stale = true
		account.Error = safeError(err)
		return account
	}

	account.Windows = windows
	account.Status = quota.StatusForWindows(windows)
	observedAt := client.now().UTC()
	account.LastFreshAt = &observedAt
	for index := range account.Windows {
		if account.Windows[index].ObservedAt == nil {
			value := observedAt
			account.Windows[index].ObservedAt = &value
		}
	}
	return account
}

func opaqueAuthIdentity(authIndex string) string {
	authIndex = strings.TrimSpace(authIndex)
	if authIndex == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(authIndex))
	return fmt.Sprintf("%x", sum[:])
}

func quotaProviderSupported(provider string) bool {
	switch provider {
	case "codex", "claude", "gemini-cli", "antigravity", "kimi", "xai":
		return true
	default:
		return false
	}
}

func (client *Client) listAuthFiles(ctx context.Context) ([]map[string]any, error) {
	var payload struct {
		Files []map[string]any `json:"files"`
	}
	if err := client.managementJSON(ctx, http.MethodGet, "/v0/management/auth-files", nil, &payload); err != nil {
		return nil, fmt.Errorf("list CLIProxyAPI auth files: %w", err)
	}
	return payload.Files, nil
}

// xaiUserID returns the non-secret account subject required by Grok's billing
// endpoint. CLIProxyAPI intentionally omits it from the auth-file listing, so
// read only that field through its authenticated download endpoint. Access and
// refresh tokens remain inside CLIProxyAPI and are never returned to callers.
func (client *Client) xaiUserID(ctx context.Context, entry map[string]any) (string, error) {
	for _, candidate := range []any{
		entry["sub"],
		nested(entry, "metadata", "sub"),
		nested(entry, "attributes", "sub"),
	} {
		if subject := strings.TrimSpace(firstString(candidate)); subject != "" {
			return subject, nil
		}
	}

	name := strings.TrimSpace(firstString(entry["name"]))
	if name == "" {
		return "", errors.New("xAI OAuth entry has no credential name")
	}
	var credential struct {
		Subject string `json:"sub"`
	}
	path := "/v0/management/auth-files/download?name=" + url.QueryEscape(name)
	if err := client.managementJSON(ctx, http.MethodGet, path, nil, &credential); err != nil {
		return "", fmt.Errorf("read xAI OAuth account subject: %w", err)
	}
	subject := strings.TrimSpace(credential.Subject)
	if subject == "" {
		return "", errors.New("xAI OAuth credential has no account subject")
	}
	return subject, nil
}

func (client *Client) apiCall(ctx context.Context, request apiCallRequest) (apiCallResponse, error) {
	var response apiCallResponse
	if err := client.managementJSON(ctx, http.MethodPost, "/v0/management/api-call", request, &response); err != nil {
		return response, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response, fmt.Errorf("provider quota endpoint returned HTTP %d (%s)", response.StatusCode, statusText(response.StatusCode))
	}
	return response, nil
}

func (client *Client) managementJSON(ctx context.Context, method, path string, body any, output any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return errors.New("build CLIProxyAPI management request")
		}
		reader = bytes.NewReader(encoded)
	}

	request, err := http.NewRequestWithContext(ctx, method, client.baseURL+path, reader)
	if err != nil {
		return errors.New("build CLIProxyAPI management request")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+client.managementKey)
	request.Header.Set("User-Agent", "ai-quota-frame/1")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := client.httpClient.Do(request)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return fmt.Errorf("CLIProxyAPI management request timed out: %w", context.DeadlineExceeded)
		case errors.Is(err, context.Canceled):
			return fmt.Errorf("CLIProxyAPI management request cancelled: %w", context.Canceled)
		default:
			return errors.New("CLIProxyAPI management request failed")
		}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// Drain a bounded amount for connection reuse, but never surface the
		// management response body: it may contain auth-file or upstream details.
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return fmt.Errorf("management API returned HTTP %d (%s)", response.StatusCode, statusText(response.StatusCode))
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes))
	decoder.UseNumber()
	if err := decoder.Decode(output); err != nil {
		return errors.New("CLIProxyAPI management response was invalid")
	}
	return nil
}

func providerRank(provider string) int {
	switch provider {
	case "codex":
		return 0
	case "claude":
		return 1
	case "gemini-cli":
		return 2
	case "antigravity":
		return 3
	case "kimi":
		return 4
	case "xai":
		return 5
	default:
		return 9
	}
}

func safeError(err error) string {
	if err == nil {
		return ""
	}
	return compactText(err.Error(), 180)
}

func statusText(status int) string {
	if text := http.StatusText(status); text != "" {
		return text
	}
	return "unknown status"
}

func compactText(value string, limit int) string {
	value = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.TrimSpace(value))
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > limit {
		return value[:limit] + "..."
	}
	return value
}
