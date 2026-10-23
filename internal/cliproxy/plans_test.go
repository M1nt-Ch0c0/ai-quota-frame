package cliproxy

import (
	"context"
	"net/http"
	"testing"
)

func TestFetchAccountReadsSubscriptionWithoutLosingQuota(t *testing.T) {
	for _, test := range []struct {
		name, provider, profile, plan string
		profileStatus                 int
	}{
		{"claude pro", "claude", `{"organization":{"organization_type":"claude_pro"}}`, "pro", 200},
		{"grok heavy", "xai", `{"subscription_tier_display":"SuperGrok Heavy"}`, "supergrok heavy", 200},
		{"claude unavailable", "claude", `{}`, "", 503},
		{"grok unavailable", "xai", `{}`, "", 503},
		{"unknown organization", "claude", `{"organization":{"organization_type":"unknown"}}`, "", 200},
		{"malformed settings", "xai", `not json`, "", 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			planRequests := 0
			client := providerFixtureClient(t, func(request apiCallRequest) apiCallResponse {
				switch request.URL {
				case claudeUsageURL:
					return providerSuccessResponse(providerFixtureBody(t, "claude_usage.json"))
				case xaiBillingURL:
					return providerSuccessResponse(providerFixtureBody(t, "xai_billing_fresh_week.json"))
				case claudeProfileURL, xaiSettingsURL:
					planRequests++
					if request.Method != http.MethodGet || request.Header["Authorization"] != "Bearer $TOKEN$" {
						t.Errorf("plan request must use the existing OAuth relay")
					}
					if test.provider == "xai" && request.Header["x-userid"] != "fixture-user-id" {
						t.Error("Grok settings request is missing the account subject")
					}
					return apiCallResponse{StatusCode: test.profileStatus, Body: test.profile}
				default:
					t.Errorf("unexpected provider endpoint %s", request.URL)
					return providerErrorResponse(http.StatusNotFound)
				}
			})
			account := client.fetchAccount(context.Background(), map[string]any{
				"provider": test.provider, "auth_index": "fixture-auth", "sub": "fixture-user-id",
			})
			if account.Plan != test.plan || planRequests != 1 {
				t.Fatalf("plan = %q, requests = %d; want %q, 1", account.Plan, planRequests, test.plan)
			}
			if len(account.Windows) == 0 || account.Stale || account.Error != "" || account.Status == "error" {
				t.Fatalf("optional plan lookup invalidated usable quota: %#v", account)
			}
		})
	}
}

func TestFetchAccountKeepsKnownPlanWithoutExtraRequests(t *testing.T) {
	requests := 0
	client := providerFixtureClient(t, func(request apiCallRequest) apiCallResponse {
		requests++
		if request.URL != claudeUsageURL {
			t.Errorf("known plan unexpectedly requested %s", request.URL)
		}
		return providerSuccessResponse(providerFixtureBody(t, "claude_usage.json"))
	})
	account := client.fetchAccount(context.Background(), map[string]any{
		"provider": "claude", "auth_index": "fixture-auth", "plan_type": "pro",
	})
	if account.Plan != "pro" || requests != 1 {
		t.Fatalf("known plan = %q, requests = %d; want pro, 1", account.Plan, requests)
	}
}
