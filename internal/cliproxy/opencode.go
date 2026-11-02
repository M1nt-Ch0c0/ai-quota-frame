package cliproxy

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/quota"
)

// The OpenCode Go plugin registers a CPA quota provider that reads the
// official allowance (rolling 5h, weekly, monthly). CPA resolves the account's
// token and organization, so no OpenCode secret leaves the proxy.
const (
	quotaFetchPath   = "/v0/management/quota/fetch"
	openCodePluginID = "opencode-go-oauth"
)

func (client *Client) fetchOpenCode(ctx context.Context, authIndex string, entry map[string]any) ([]quota.Window, string, error) {
	plan := openCodePlan(entry)
	request := map[string]string{"auth_index": authIndex, "plugin_id": openCodePluginID}
	var payload map[string]any
	if err := client.managementJSON(ctx, http.MethodPost, quotaFetchPath, request, &payload); err != nil {
		return nil, plan, err
	}
	if plan == "" {
		plan = openCodePlanTier(firstString(payload["plan"], payload["plan_type"]))
	}
	windows := parseAntigravitySummary(payload)
	if len(windows) == 0 {
		return nil, plan, errors.New("OpenCode quota response contained no usable windows")
	}
	return windows, plan, nil
}

// The official usage API has no plan. Like the plugin, read only an explicit
// per-credential tier and never infer one from the allowance.
func openCodePlan(entry map[string]any) string {
	for _, value := range []any{
		entry["plan"], entry["plan_type"], entry["planType"], entry["note"],
		nested(entry, "metadata", "plan"), nested(entry, "metadata", "plan_type"), nested(entry, "metadata", "note"),
		nested(entry, "attributes", "plan"), nested(entry, "attributes", "plan_type"), nested(entry, "attributes", "header:x-opencode-plan"),
		nested(entry, "headers", "x-opencode-plan"), nested(entry, "headers", "X-Opencode-Plan"),
	} {
		if plan := openCodePlanTier(firstString(value)); plan != "" {
			return plan
		}
	}
	return ""
}

func openCodePlanTier(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case s == "go-plus" || s == "goplus" || s == "plus" || strings.Contains(s, "go-plus") || strings.Contains(s, "go plus") || strings.Contains(s, "$40"):
		return "go plus"
	case s == "go" || strings.Contains(s, "opencode go") || strings.Contains(s, "$10"):
		return "go"
	default:
		return ""
	}
}

// googleTierPlan maps Code Assist / Antigravity tiers (ids such as
// g1-pro-tier or names such as "Google AI Ultra") to a short plan.
func googleTierPlan(id, name any) string {
	for _, raw := range []string{firstString(id), firstString(name)} {
		s := strings.ToLower(strings.TrimSpace(raw))
		switch {
		case s == "":
			continue
		case strings.Contains(s, "ultra"):
			return "ultra"
		case strings.Contains(s, "pro"):
			return "pro"
		case strings.Contains(s, "standard"):
			return "standard"
		case strings.Contains(s, "enterprise"):
			return "enterprise"
		case strings.Contains(s, "free"):
			return "free"
		case strings.Contains(s, "legacy"):
			return "legacy"
		}
	}
	return strings.ToLower(strings.TrimSpace(firstString(name, id)))
}
