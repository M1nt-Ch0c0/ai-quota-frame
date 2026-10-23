package cliproxy

import (
	"context"
	"net/http"
	"strings"
)

const (
	claudeProfileURL = "https://api.anthropic.com/api/oauth/profile"
	xaiSettingsURL   = "https://cli-chat-proxy.grok.com/v1/settings"
)

// fetchSubscriptionPlan fills metadata that quota endpoints can omit. A
// missing or unavailable plan must never invalidate successfully read quota.
func (client *Client) fetchSubscriptionPlan(ctx context.Context, provider, authIndex string, entry map[string]any) string {
	request := apiCallRequest{
		AuthIndex: authIndex,
		Method:    http.MethodGet,
		Header: map[string]string{
			"Authorization": "Bearer $TOKEN$",
			"Accept":        "application/json",
		},
	}
	switch provider {
	case "claude":
		request.URL = claudeProfileURL
		request.Header["anthropic-beta"] = "oauth-2025-04-20"
	case "xai":
		userID, err := client.xaiUserID(ctx, entry)
		if err != nil {
			return ""
		}
		request.URL = xaiSettingsURL
		request.Header["X-XAI-Token-Auth"] = "xai-grok-cli"
		request.Header["x-userid"] = userID
		request.Header["x-grok-client-version"] = xaiClientVersion
		request.Header["x-grok-client-identifier"] = "grok-shell"
	default:
		return ""
	}
	response, err := client.apiCall(ctx, request)
	if err != nil {
		return ""
	}
	payload, err := parseJSONBody(response.Body)
	if err != nil {
		return ""
	}
	if provider == "xai" {
		return strings.ToLower(strings.TrimSpace(firstString(payload["subscription_tier_display"])))
	}
	organization := strings.ToLower(strings.TrimSpace(firstString(nested(payload, "organization", "organization_type"))))
	switch organization {
	case "claude_pro", "claude_max", "claude_team", "claude_enterprise", "claude_free":
		return strings.TrimPrefix(organization, "claude_")
	default:
		return ""
	}
}
