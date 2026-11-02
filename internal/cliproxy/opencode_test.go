package cliproxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func openCodeFixtureClient(t *testing.T, status int, body string) (*Client, *map[string]string) {
	t.Helper()
	var got map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != quotaFetchPath {
			t.Errorf("management request = %s %s", request.Method, request.URL.Path)
			http.Error(response, "unexpected", http.StatusNotFound)
			return
		}
		if err := json.NewDecoder(request.Body).Decode(&got); err != nil {
			t.Errorf("decode quota fetch request: %v", err)
		}
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(status)
		_, _ = response.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL, "fixture-management-key", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return client, &got
}

func TestFetchOpenCodeUsesPluginQuotaProvider(t *testing.T) {
	client, request := openCodeFixtureClient(t, http.StatusOK, string(providerFixtureBody(t, "opencode_quota_fetch.json")))
	windows, plan, err := client.fetchOpenCode(context.Background(), "oc-auth", map[string]any{"note": "OpenCode Go Plus"})
	if err != nil {
		t.Fatal(err)
	}
	if (*request)["auth_index"] != "oc-auth" || (*request)["plugin_id"] != openCodePluginID {
		t.Fatalf("request = %v", *request)
	}
	if plan != "go plus" {
		t.Fatalf("plan = %q", plan)
	}
	fiveHour := requireProviderWindow(t, windows, "5h")
	assertProviderPercent(t, fiveHour.RemainingPercent, 98, "OpenCode 5h remaining")
	assertProviderReset(t, fiveHour.ResetsAt, "2026-11-01T21:13:33Z")
	weekly := requireProviderWindow(t, windows, "7d")
	assertProviderPercent(t, weekly.RemainingPercent, 99, "OpenCode weekly remaining")
	monthly := requireProviderWindow(t, windows, "30d")
	assertProviderPercent(t, monthly.UsedPercent, 25, "OpenCode monthly used")
}

func TestFetchOpenCodeFailsClosed(t *testing.T) {
	client, _ := openCodeFixtureClient(t, http.StatusBadGateway, `{"error":"failed to fetch quota"}`)
	if _, _, err := client.fetchOpenCode(context.Background(), "oc-auth", map[string]any{}); err == nil {
		t.Fatal("upstream failure produced quota")
	}
	client, _ = openCodeFixtureClient(t, http.StatusOK, `{"groups":[]}`)
	if windows, _, err := client.fetchOpenCode(context.Background(), "oc-auth", map[string]any{}); err == nil || windows != nil {
		t.Fatal("empty quota was accepted")
	}
}

func TestOpenCodePlanUsesOnlyExplicitTier(t *testing.T) {
	for entry, want := range map[*map[string]any]string{
		{"plan": "go-plus"}:                                       "go plus",
		{"note": "OpenCode Go ($10/mo)"}:                          "go",
		{"headers": map[string]any{"x-opencode-plan": "Go Plus"}}: "go plus",
		{"metadata": map[string]any{"plan_type": "go"}}:           "go",
		{"note": "work account"}:                                  "",
		{}:                                                        "",
	} {
		if got := openCodePlan(*entry); got != want {
			t.Errorf("openCodePlan(%v) = %q, want %q", *entry, got, want)
		}
	}
}

func TestGoogleTierPlan(t *testing.T) {
	for _, c := range []struct{ id, name, want string }{
		{"g1-pro-tier", "Google AI Pro", "pro"},
		{"g1-ultra-tier", "", "ultra"},
		{"", "Google AI Ultra", "ultra"},
		{"free-tier", "Free", "free"},
		{"standard-tier", "Standard", "standard"},
		{"", "", ""},
		{"mystery-tier", "Mystery", "mystery"},
	} {
		if got := googleTierPlan(c.id, c.name); got != c.want {
			t.Errorf("googleTierPlan(%q, %q) = %q, want %q", c.id, c.name, got, c.want)
		}
	}
}
