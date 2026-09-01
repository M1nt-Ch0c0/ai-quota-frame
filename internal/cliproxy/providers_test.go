package cliproxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/quota"
)

func TestFetchCodexParsesFixturePercentAndDynamicWindows(t *testing.T) {
	body := providerFixtureBody(t, "codex_usage.json")
	client := providerFixtureClient(t, func(request apiCallRequest) apiCallResponse {
		if request.URL != codexUsageURL {
			t.Errorf("provider URL = %q, want %q", request.URL, codexUsageURL)
			return providerErrorResponse(http.StatusNotFound)
		}
		return providerSuccessResponse(body)
	})

	windows, plan, err := client.fetchCodex(context.Background(), "codex-auth", map[string]any{})
	if err != nil {
		t.Fatalf("fetchCodex() error = %v", err)
	}
	if plan != "plus" {
		t.Fatalf("plan = %q, want plus", plan)
	}
	if len(windows) != 2 {
		t.Fatalf("window count = %d, want 2", len(windows))
	}

	primary := requireProviderWindow(t, windows, "3h")
	assertProviderPercent(t, primary.UsedPercent, 37.5, "Codex 3h used")
	assertProviderPercent(t, primary.RemainingPercent, 62.5, "Codex 3h remaining")
	assertProviderReset(t, primary.ResetsAt, "2026-08-30T12:00:00Z")
	if primary.Label != "3h" || primary.Source != activeQuotaSource {
		t.Fatalf("primary label/source = %q/%q, want 3h/%s", primary.Label, primary.Source, activeQuotaSource)
	}

	secondary := requireProviderWindow(t, windows, "14d")
	assertProviderPercent(t, secondary.UsedPercent, 81, "Codex 14d used")
	assertProviderPercent(t, secondary.RemainingPercent, 19, "Codex 14d remaining")
	assertProviderReset(t, secondary.ResetsAt, "2026-09-12T00:00:00Z")
}

func TestFetchClaudeParsesFixturePercentAndScopedWindows(t *testing.T) {
	body := providerFixtureBody(t, "claude_usage.json")
	client := providerFixtureClient(t, func(request apiCallRequest) apiCallResponse {
		if request.URL != claudeUsageURL {
			t.Errorf("provider URL = %q, want %q", request.URL, claudeUsageURL)
			return providerErrorResponse(http.StatusNotFound)
		}
		return providerSuccessResponse(body)
	})

	windows, _, err := client.fetchClaude(context.Background(), "claude-auth")
	if err != nil {
		t.Fatalf("fetchClaude() error = %v", err)
	}
	if len(windows) != 4 {
		t.Fatalf("window count = %d, want 4", len(windows))
	}

	fiveHour := requireProviderWindow(t, windows, "5h")
	assertProviderPercent(t, fiveHour.UsedPercent, 42, "Claude active 5h used")
	assertProviderPercent(t, fiveHour.RemainingPercent, 58, "Claude active 5h remaining")
	assertProviderReset(t, fiveHour.ResetsAt, "2026-08-30T12:30:00Z")

	sonnet := requireProviderWindow(t, windows, "sonnet-7d")
	if sonnet.Label != "Sonnet 7d" {
		t.Fatalf("Sonnet label = %q, want %q", sonnet.Label, "Sonnet 7d")
	}
	assertProviderPercent(t, sonnet.UsedPercent, 83.5, "Claude Sonnet used")

	scoped := requireProviderWindow(t, windows, "fable-7d")
	if scoped.Label != "Fable 5 7d" {
		t.Fatalf("scoped label = %q, want %q", scoped.Label, "Fable 5 7d")
	}
	assertProviderPercent(t, scoped.UsedPercent, 25, "Claude scoped used")
	for _, window := range windows {
		if window.ID == "iguana-necktie" {
			t.Fatal("legacy Iguana window was not suppressed when a modern Fable limit exists")
		}
	}
}

func TestFetchGeminiParsesRemainingFractionAndPrefersPaidTier(t *testing.T) {
	loadBody := providerFixtureBody(t, "google_load_paid_tier.json")
	quotaBody := providerFixtureBody(t, "gemini_quota.json")
	client := providerFixtureClient(t, func(request apiCallRequest) apiCallResponse {
		switch request.URL {
		case geminiLoadURL:
			return providerSuccessResponse(loadBody)
		case geminiQuotaURL:
			var data map[string]any
			if err := json.Unmarshal([]byte(request.Data), &data); err != nil {
				t.Errorf("decode Gemini request data: %v", err)
			} else if got := firstString(data["project"]); got != "fixture-project" {
				t.Errorf("Gemini request project = %q, want fixture-project", got)
			}
			return providerSuccessResponse(quotaBody)
		default:
			t.Errorf("unexpected provider URL %q", request.URL)
			return providerErrorResponse(http.StatusNotFound)
		}
	})

	windows, plan, err := client.fetchGemini(context.Background(), "gemini-auth", map[string]any{})
	if err != nil {
		t.Fatalf("fetchGemini() error = %v", err)
	}
	if plan != "standard-tier" {
		t.Fatalf("plan = %q, want paidTier id standard-tier (currentTier is free-tier)", plan)
	}

	pro := requireProviderWindow(t, windows, "pro")
	assertProviderPercent(t, pro.RemainingPercent, 25, "Gemini Pro remaining")
	assertProviderPercent(t, pro.UsedPercent, 75, "Gemini Pro used")
	assertProviderReset(t, pro.ResetsAt, "2026-08-30T12:45:00Z")

	flash := requireProviderWindow(t, windows, "flash")
	assertProviderPercent(t, flash.RemainingPercent, 80, "Gemini Flash remaining")
	other := requireProviderWindow(t, windows, "other")
	assertProviderPercent(t, other.RemainingPercent, 55, "Gemini other remaining")
}

func TestFetchAntigravityUsesQuotaSummaryAndConservativeWindowAggregation(t *testing.T) {
	const dailySummaryURL = "https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary"
	loadBody := providerFixtureBody(t, "google_load_paid_tier.json")
	quotaBody := providerFixtureBody(t, "antigravity_quota_summary.json")
	var quotaURLs []string
	client := providerFixtureClient(t, func(request apiCallRequest) apiCallResponse {
		switch {
		case strings.HasSuffix(request.URL, ":loadCodeAssist"):
			return providerSuccessResponse(loadBody)
		case strings.HasSuffix(request.URL, ":retrieveUserQuotaSummary"):
			quotaURLs = append(quotaURLs, request.URL)
			if request.URL != dailySummaryURL {
				return providerErrorResponse(http.StatusBadGateway)
			}
			var data map[string]any
			if err := json.Unmarshal([]byte(request.Data), &data); err != nil {
				t.Errorf("decode Antigravity request data: %v", err)
			} else if got := firstString(data["project"]); got != "fixture-project" {
				t.Errorf("Antigravity request project = %q, want fixture-project", got)
			}
			return providerSuccessResponse(quotaBody)
		default:
			t.Errorf("unexpected Antigravity provider URL %q", request.URL)
			return providerErrorResponse(http.StatusNotFound)
		}
	})

	windows, plan, err := client.fetchAntigravity(context.Background(), "antigravity-auth", map[string]any{})
	if err != nil {
		t.Fatalf("fetchAntigravity() error = %v", err)
	}
	if plan != "standard-tier" {
		t.Fatalf("plan = %q, want standard-tier", plan)
	}
	if len(quotaURLs) != 1 || quotaURLs[0] != dailySummaryURL {
		t.Fatalf("quota summary calls = %v, want [%s]", quotaURLs, dailySummaryURL)
	}

	fiveHour := requireProviderWindow(t, windows, "5h")
	assertProviderPercent(t, fiveHour.RemainingPercent, 40, "Antigravity 5h remaining")
	assertProviderPercent(t, fiveHour.UsedPercent, 60, "Antigravity 5h used")
	assertProviderReset(t, fiveHour.ResetsAt, "2026-08-30T11:00:00Z")

	weekly := requireProviderWindow(t, windows, "7d")
	assertProviderPercent(t, weekly.RemainingPercent, 72, "Antigravity weekly remaining")
	assertProviderPercent(t, weekly.UsedPercent, 28, "Antigravity weekly used")
	assertProviderReset(t, weekly.ResetsAt, "2026-09-05T00:00:00Z")
}

func TestProviderUnitValidatorsRejectSchemaDrift(t *testing.T) {
	for _, value := range []any{-0.01, 1.01, "25", "NaN"} {
		if parsed, ok := fraction(value); ok {
			t.Errorf("fraction(%v) = %v, true; want rejected", value, parsed)
		}
	}
	for _, value := range []any{-1, 100.01, "NaN", "+Inf"} {
		if parsed, ok := percentage(value); ok {
			t.Errorf("percentage(%v) = %v, true; want rejected", value, parsed)
		}
	}
	for _, value := range []any{0, 0.42, 1, "1"} {
		if _, ok := fraction(value); !ok {
			t.Errorf("fraction(%v) rejected valid value", value)
		}
	}
	for _, value := range []any{0, 42, 100, "42"} {
		if _, ok := percentage(value); !ok {
			t.Errorf("percentage(%v) rejected valid value", value)
		}
	}
}

func providerFixtureClient(t *testing.T, responder func(apiCallRequest) apiCallResponse) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v0/management/api-call" {
			t.Errorf("management request = %s %s, want POST /v0/management/api-call", request.Method, request.URL.Path)
			http.Error(response, "unexpected management request", http.StatusNotFound)
			return
		}
		if got := request.Header.Get("Authorization"); got != "Bearer fixture-management-key" {
			t.Errorf("management Authorization = %q", got)
		}
		var call apiCallRequest
		if err := json.NewDecoder(request.Body).Decode(&call); err != nil {
			t.Errorf("decode management api-call request: %v", err)
			http.Error(response, "invalid request", http.StatusBadRequest)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(response).Encode(responder(call)); err != nil {
			t.Errorf("encode management api-call response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	client, err := NewClient(server.URL, "fixture-management-key", 2*time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}

func providerFixtureBody(t *testing.T, name string) string {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(payload)
}

func providerFixtureMap(t *testing.T, name string) map[string]any {
	t.Helper()
	payload, err := parseJSONBody(providerFixtureBody(t, name))
	if err != nil {
		t.Fatalf("parse fixture %s: %v", name, err)
	}
	return payload
}

func providerSuccessResponse(body string) apiCallResponse {
	return apiCallResponse{StatusCode: http.StatusOK, Body: body}
}

func providerErrorResponse(status int) apiCallResponse {
	return apiCallResponse{StatusCode: status, Body: `{"error":"fixture provider error"}`}
}

func requireProviderWindow(t *testing.T, windows []quota.Window, id string) quota.Window {
	t.Helper()
	for _, window := range windows {
		if window.ID == id {
			return window
		}
	}
	t.Fatalf("window %q not found in %+v", id, windows)
	return quota.Window{}
}

func assertProviderPercent(t *testing.T, got *float64, want float64, label string) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s = nil, want %.4g", label, want)
	}
	if delta := *got - want; delta < -0.000001 || delta > 0.000001 {
		t.Fatalf("%s = %.8g, want %.8g", label, *got, want)
	}
}

func assertProviderReset(t *testing.T, got *time.Time, want string) {
	t.Helper()
	if got == nil {
		t.Fatalf("reset = nil, want %s", want)
	}
	if got.Format(time.RFC3339) != want {
		t.Fatalf("reset = %s, want %s", got.Format(time.RFC3339), want)
	}
}

func TestFetchKimiParsesFixtureStringNumbersAndWindows(t *testing.T) {
	body := providerFixtureBody(t, "kimi_usages.json")
	client := providerFixtureClient(t, func(request apiCallRequest) apiCallResponse {
		if request.URL != kimiUsageURL {
			t.Errorf("provider URL = %q, want %q", request.URL, kimiUsageURL)
			return providerErrorResponse(http.StatusNotFound)
		}
		if request.Header["Authorization"] != "Bearer $TOKEN$" {
			t.Errorf("Authorization header = %q, want Bearer $TOKEN$ placeholder", request.Header["Authorization"])
		}
		return providerSuccessResponse(body)
	})

	windows, plan, err := client.fetchKimi(context.Background(), "kimi-auth")
	if err != nil {
		t.Fatalf("fetchKimi() error = %v", err)
	}
	if plan != "advanced" {
		t.Fatalf("plan = %q, want advanced", plan)
	}
	if len(windows) != 2 {
		t.Fatalf("window count = %d, want 2", len(windows))
	}

	weekly := requireProviderWindow(t, windows, "7d")
	assertProviderPercent(t, weekly.RemainingPercent, 100, "Kimi 7d remaining")
	assertProviderReset(t, weekly.ResetsAt, "2026-09-08T07:09:41Z")

	primary := requireProviderWindow(t, windows, "5h")
	assertProviderPercent(t, primary.UsedPercent, 15, "Kimi 5h used")
	assertProviderPercent(t, primary.RemainingPercent, 85, "Kimi 5h remaining")
	assertProviderReset(t, primary.ResetsAt, "2026-09-01T09:09:41Z")
	if primary.Source != activeQuotaSource {
		t.Fatalf("5h source = %q, want %s", primary.Source, activeQuotaSource)
	}
}

func TestFetchKimiRejectsMissingNumbersInsteadOfFabricatingZero(t *testing.T) {
	client := providerFixtureClient(t, func(request apiCallRequest) apiCallResponse {
		return providerSuccessResponse(`{"usage":{"resetTime":"2026-09-08T00:00:00Z"}}`)
	})
	_, _, err := client.fetchKimi(context.Background(), "kimi-auth")
	if err == nil {
		t.Fatal("fetchKimi() succeeded on a response without any numeric quota fields")
	}
}

func TestFetchXAIParsesOfficialBillingUsagePool(t *testing.T) {
	body := providerFixtureBody(t, "xai_billing.json")
	client := providerFixtureClient(t, func(request apiCallRequest) apiCallResponse {
		if request.URL != xaiBillingURL {
			t.Errorf("provider URL = %q, want %q", request.URL, xaiBillingURL)
			return providerErrorResponse(http.StatusNotFound)
		}
		for key, want := range map[string]string{
			"Authorization":            "Bearer $TOKEN$",
			"X-XAI-Token-Auth":         "xai-grok-cli",
			"x-userid":                 "fixture-user-id",
			"x-grok-client-version":    xaiClientVersion,
			"x-grok-client-identifier": "grok-shell",
		} {
			if got := request.Header[key]; got != want {
				t.Errorf("%s header = %q, want %q", key, got, want)
			}
		}
		return providerSuccessResponse(body)
	})

	windows, plan, err := client.fetchXAI(context.Background(), "xai-auth", map[string]any{"sub": "fixture-user-id"})
	if err != nil {
		t.Fatalf("fetchXAI() error = %v", err)
	}
	if plan != "supergrok" {
		t.Fatalf("plan = %q, want supergrok", plan)
	}
	if len(windows) != 1 {
		t.Fatalf("window count = %d, want 1", len(windows))
	}
	weekly := requireProviderWindow(t, windows, "7d")
	assertProviderPercent(t, weekly.UsedPercent, 36.5, "Grok 7d used")
	assertProviderPercent(t, weekly.RemainingPercent, 63.5, "Grok 7d remaining")
	assertProviderReset(t, weekly.ResetsAt, "2026-09-06T00:00:00Z")
}
