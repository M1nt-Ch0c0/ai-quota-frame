// Package usage reads today's token consumption and estimated cost from a
// local cpa-manager-plus collector. The collector holds the data; this client
// only ever sees the aggregated dashboard summary.
package usage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/quota"
)

const (
	summaryPath    = "/v0/management/dashboard/summary"
	usageSource    = "cpa-manager-plus"
	usageCurrency  = "$"
	maxSummaryBody = 1 << 20
)

type Client struct {
	baseURL    string
	adminKey   string
	httpClient *http.Client
	location   *time.Location
}

func NewClient(baseURL, adminKey string, timeout time.Duration, location *time.Location) (*Client, error) {
	trimmedURL := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	parsed, err := url.Parse(trimmedURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid usage collector base URL %q", baseURL)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("usage collector base URL must use http or https")
	}
	if parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
		return nil, errors.New("plaintext usage collector access is allowed only on loopback; use HTTPS for a remote host")
	}
	if parsed.User != nil {
		return nil, errors.New("usage collector base URL must not contain user credentials")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("usage collector base URL must not contain a query or fragment")
	}
	if strings.TrimSpace(adminKey) == "" {
		return nil, errors.New("usage collector admin key is empty")
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if location == nil {
		location = time.Local
	}
	return &Client{
		baseURL: trimmedURL,
		adminKey: strings.TrimSpace(adminKey),
		httpClient: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		location: location,
	}, nil
}

func (client *Client) FetchUsage(ctx context.Context) (*quota.Usage, error) {
	midnight := todayStart(time.Now().In(client.location), client.location)
	requestURL := fmt.Sprintf("%s%s?today_start_ms=%d", client.baseURL, summaryPath, midnight.UnixMilli())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, errors.New("build usage collector request")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+client.adminKey)
	request.Header.Set("User-Agent", "ai-quota-frame/1")

	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, errors.New("usage collector request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return nil, fmt.Errorf("usage collector returned HTTP %d", response.StatusCode)
	}

	var payload struct {
		Today struct {
			TotalTokens json.Number `json:"total_tokens"`
			TotalCost   json.Number `json:"total_cost"`
		} `json:"today"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxSummaryBody))
	if err := decoder.Decode(&payload); err != nil {
		return nil, errors.New("usage collector response was invalid")
	}
	tokens, err := payload.Today.TotalTokens.Int64()
	if err != nil {
		return nil, errors.New("usage collector response had no valid total_tokens")
	}
	cost, err := payload.Today.TotalCost.Float64()
	if err != nil {
		return nil, errors.New("usage collector response had no valid total_cost")
	}
	return &quota.Usage{
		TodayTokens: tokens,
		TodayCost:   cost,
		Currency:    usageCurrency,
		Source:      usageSource,
	}, nil
}

func todayStart(now time.Time, location *time.Location) time.Time {
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "localhost" {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}
