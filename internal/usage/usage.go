// Package usage reads token consumption and estimated cost from a
// local cpa-manager-plus collector. The collector holds the data; this client
// only ever sees aggregated dashboard summaries.
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
	"sync"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/quota"
)

const (
	summaryPath    = "/v0/management/dashboard/summary"
	usageSource    = "cpa-manager-plus"
	usageCurrency  = "$"
	maxSummaryBody = 1 << 20
	usageDays      = 7
)

type Client struct {
	baseURL    string
	bearer     func(context.Context) (string, error)
	httpClient *http.Client
	location   *time.Location
	now        func() time.Time
}

func NewClient(baseURL, adminKey string, timeout time.Duration, location *time.Location) (*Client, error) {
	key := strings.TrimSpace(adminKey)
	if key == "" {
		return nil, errors.New("usage collector admin key is empty")
	}
	return NewClientWithBearer(baseURL, func(context.Context) (string, error) { return key, nil }, timeout, location)
}

// NewClientWithBearer authenticates with a credential obtained per request,
// such as a short-lived ID token accepted by a gateway in front of the API.
func NewClientWithBearer(baseURL string, bearer func(context.Context) (string, error), timeout time.Duration, location *time.Location) (*Client, error) {
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
	if bearer == nil {
		return nil, errors.New("usage collector credential is missing")
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if location == nil {
		location = time.Local
	}
	return &Client{
		baseURL: trimmedURL,
		bearer:  bearer,
		httpClient: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		location: location,
		now:      time.Now,
	}, nil
}

func (client *Client) FetchUsage(ctx context.Context) (*quota.Usage, error) {
	now := client.now().In(client.location)
	today := todayStart(now, client.location)
	type windowResult struct {
		tokens int64
		cost   float64
		err    error
	}
	results := make([]windowResult, usageDays)
	days := make([]quota.UsageDay, usageDays)

	var group sync.WaitGroup
	for index := 0; index < usageDays; index++ {
		index := index
		start := today.AddDate(0, 0, index-(usageDays-1))
		end := start.AddDate(0, 0, 1)
		if index == usageDays-1 {
			end = now
		}
		days[index] = quota.UsageDay{Date: start.Format("2006-01-02")}
		group.Add(1)
		go func() {
			defer group.Done()
			tokens, cost, err := client.fetchWindow(ctx, start, end)
			results[index] = windowResult{tokens: tokens, cost: cost, err: err}
		}()
	}
	group.Wait()
	if err := results[usageDays-1].err; err != nil {
		return nil, err
	}
	for index := range days {
		if results[index].err != nil {
			continue
		}
		days[index].Tokens = results[index].tokens
		days[index].Cost = results[index].cost
	}
	todayDay := days[usageDays-1]
	return &quota.Usage{
		TodayTokens: todayDay.Tokens,
		TodayCost:   todayDay.Cost,
		Currency:    usageCurrency,
		Source:      usageSource,
		Days:        days,
	}, nil
}

func (client *Client) fetchWindow(ctx context.Context, start, end time.Time) (int64, float64, error) {
	if !end.After(start) {
		end = start.Add(time.Second)
	}
	requestURL := fmt.Sprintf("%s%s?today_start_ms=%d&now_ms=%d",
		client.baseURL, summaryPath, start.UnixMilli(), end.UnixMilli())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return 0, 0, errors.New("build usage collector request")
	}
	token, err := client.bearer(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("obtain usage collector credential: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("User-Agent", "ai-quota-frame/1")

	response, err := client.httpClient.Do(request)
	if err != nil {
		return 0, 0, errors.New("usage collector request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return 0, 0, fmt.Errorf("usage collector returned HTTP %d", response.StatusCode)
	}

	var payload struct {
		Today struct {
			TotalTokens json.Number `json:"total_tokens"`
			TotalCost   json.Number `json:"total_cost"`
		} `json:"today"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxSummaryBody))
	if err := decoder.Decode(&payload); err != nil {
		return 0, 0, errors.New("usage collector response was invalid")
	}
	tokens, err := payload.Today.TotalTokens.Int64()
	if err != nil {
		return 0, 0, errors.New("usage collector response had no valid total_tokens")
	}
	cost, err := payload.Today.TotalCost.Float64()
	if err != nil {
		return 0, 0, errors.New("usage collector response had no valid total_cost")
	}
	return tokens, cost, nil
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
