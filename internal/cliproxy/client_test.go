package cliproxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAPICallRejectsCLIProxyInnerStatusCode(t *testing.T) {
	client := providerFixtureClient(t, func(apiCallRequest) apiCallResponse {
		return apiCallResponse{
			StatusCode: http.StatusTooManyRequests,
			Body:       `{"error":"quota endpoint rate limited"}`,
		}
	})

	_, err := client.apiCall(context.Background(), apiCallRequest{
		AuthIndex: "fixture-auth",
		Method:    http.MethodGet,
		URL:       "https://provider.example.test/quota",
	})
	if err == nil {
		t.Fatal("apiCall() error = nil, want inner HTTP 429 error")
	}
	if !strings.Contains(err.Error(), "HTTP 429") {
		t.Fatalf("apiCall() error = %q, want it to mention inner HTTP 429", err)
	}
	if strings.Contains(err.Error(), "quota endpoint rate limited") {
		t.Fatalf("apiCall() error leaked provider response body: %q", err)
	}
}

func TestManagementErrorDoesNotExposeResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		http.Error(response, `secret auth_index and upstream body`, http.StatusInternalServerError)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "management-key", time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	var output map[string]any
	err = client.managementJSON(context.Background(), http.MethodGet, "/v0/management/auth-files", nil, &output)
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("managementJSON() error = %v", err)
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "auth_index") {
		t.Fatalf("managementJSON() error leaked response body: %q", err)
	}
}

func TestNewClientRejectsCredentialedOrAmbiguousBaseURL(t *testing.T) {
	for _, baseURL := range []string{
		"http://user:pass@127.0.0.1:8317",
		"http://127.0.0.1:8317?redirect=evil",
		"http://127.0.0.1:8317#fragment",
		"http://192.0.2.10:8317",
	} {
		t.Run(baseURL, func(t *testing.T) {
			if _, err := NewClient(baseURL, "management-key", time.Second); err == nil {
				t.Fatalf("NewClient(%q) error = nil", baseURL)
			}
		})
	}
}

func TestPlanOfDoesNotTreatCredentialKindAsSubscriptionPlan(t *testing.T) {
	entry := map[string]any{"account_type": "oauth"}
	if got := planOf(entry); got != "" {
		t.Fatalf("planOf(account_type=oauth) = %q, want empty", got)
	}
	entry["plan_type"] = "plus"
	if got := planOf(entry); got != "plus" {
		t.Fatalf("planOf(plan_type=plus) = %q", got)
	}
}

func TestUnsupportedOAuthProviderIsVisibleWithoutAnUpstreamCall(t *testing.T) {
	client := &Client{}
	account := client.fetchAccount(context.Background(), map[string]any{
		"provider":     "xai",
		"account_type": "oauth",
		"auth_index":   "secret-index",
		"email":        "xavier@example.com",
	})
	if account.Provider != "xai" || account.Status != "unknown" || !account.Stale || account.Warning == "" {
		t.Fatalf("unsupported OAuth account = %#v", account)
	}
	if account.RetentionID == "" || strings.Contains(account.RetentionID, "secret-index") {
		t.Fatalf("opaque retention identity = %q", account.RetentionID)
	}
}

func TestFetchFiltersAPIKeysAndSerializesUnsupportedOAuthWithoutAPICall(t *testing.T) {
	var apiCallCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Authorization"); got != "Bearer management-key" {
			t.Errorf("management Authorization = %q", got)
		}
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/v0/management/auth-files":
			response.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(response).Encode(map[string]any{
				"files": []map[string]any{
					{
						"provider":     "codex",
						"account_type": "api_key",
						"auth_index":   "api-key-auth-index",
						"email":        "api-key@example.com",
					},
					{
						"provider":     "xai",
						"account_type": "oauth",
						"auth_index":   "unsupported-oauth-auth-index",
						"email":        "oauth@example.com",
					},
				},
			}); err != nil {
				t.Errorf("encode auth-files response: %v", err)
			}
		case request.Method == http.MethodPost && request.URL.Path == "/v0/management/api-call":
			apiCallCount.Add(1)
			http.Error(response, "unsupported OAuth must not reach api-call", http.StatusInternalServerError)
		default:
			t.Errorf("unexpected management request = %s %s", request.Method, request.URL.Path)
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "management-key", time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	accounts, fetchErrors, err := client.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if got := apiCallCount.Load(); got != 0 {
		t.Fatalf("management api-call count = %d, want 0", got)
	}
	if len(fetchErrors) != 0 {
		t.Fatalf("Fetch() errors = %v, want none", fetchErrors)
	}
	if len(accounts) != 1 {
		t.Fatalf("Fetch() account count = %d, want only the unsupported OAuth account", len(accounts))
	}
	if account := accounts[0]; account.Provider != "xai" || account.Status != "unknown" || !account.Stale || account.Warning == "" {
		t.Fatalf("unsupported OAuth account = %#v", account)
	}

	payload, err := json.Marshal(struct {
		Accounts any `json:"accounts"`
	}{Accounts: accounts})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	serialized := string(payload)
	if !strings.Contains(serialized, `"provider":"xai"`) {
		t.Fatalf("serialized aggregate omitted unsupported OAuth account: %s", payload)
	}
	for _, forbidden := range []string{"api-key@example.com", "api-key-auth-index", "unsupported-oauth-auth-index"} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("serialized aggregate contains filtered or secret value %q: %s", forbidden, payload)
		}
	}
}

func TestAntigravityKeepsProviderProvenance(t *testing.T) {
	if got := providerOf(map[string]any{"provider": "antigravity"}); got != "antigravity" {
		t.Fatalf("providerOf(antigravity) = %q", got)
	}
}

func TestTransientUnavailableAccountStillUsesPassiveQuotaFallback(t *testing.T) {
	client := providerFixtureClient(t, func(apiCallRequest) apiCallResponse {
		return providerErrorResponse(http.StatusTooManyRequests)
	})
	now := time.Date(2026, 8, 30, 10, 10, 0, 0, time.UTC)
	client.now = func() time.Time { return now }
	client.SetPassiveMaxAge(time.Hour)
	account := client.fetchAccount(context.Background(), map[string]any{
		"provider":    "codex",
		"auth_index":  "codex-auth",
		"unavailable": true,
		"quota": map[string]any{
			"observed_at": "2026-08-30T10:00:00Z",
			"signals": map[string]any{
				"X-Codex-Primary-Used-Percent": "100",
			},
		},
	})
	if account.Status != "exhausted" || !account.Stale || len(account.Windows) != 1 {
		t.Fatalf("unavailable account fallback = %#v", account)
	}
}
