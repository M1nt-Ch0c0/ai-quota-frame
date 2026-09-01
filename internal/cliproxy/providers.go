package cliproxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/quota"
)

const (
	codexUsageURL      = "https://chatgpt.com/backend-api/wham/usage"
	claudeUsageURL     = "https://api.anthropic.com/api/oauth/usage"
	geminiLoadURL      = "https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist"
	geminiQuotaURL     = "https://cloudcode-pa.googleapis.com/v1internal:retrieveUserQuota"
	antigravityLoadURL = "https://daily-cloudcode-pa.googleapis.com/v1internal:loadCodeAssist"
	antigravityDaily   = "https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary"
	antigravitySandbox = "https://daily-cloudcode-pa.sandbox.googleapis.com/v1internal:retrieveUserQuotaSummary"
	antigravityURL     = "https://cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary"
	kimiUsageURL       = "https://api.kimi.com/coding/v1/usages"
	activeQuotaSource  = "oauth_internal_endpoint"
	passiveQuotaSource = "cliproxy_headers"
)


// fetchKimi reads the Kimi For Coding quota. Verified live against
// api.kimi.com on 2026-09-01: the response carries a top-level weekly usage
// bucket plus a limits[] array whose 300-minute entry is the 5-hour window.
// Numeric fields are strings; missing fields must not fabricate percentages.
func (client *Client) fetchKimi(ctx context.Context, authIndex string) ([]quota.Window, string, error) {
	response, err := client.apiCall(ctx, apiCallRequest{
		AuthIndex: authIndex,
		Method:    http.MethodGet,
		URL:       kimiUsageURL,
		Header: map[string]string{
			"Authorization": "Bearer $TOKEN$",
			"Accept":        "application/json",
			"User-Agent":    "ai-quota-frame/1",
		},
	})
	if err != nil {
		return nil, "", err
	}
	payload, err := parseJSONBody(response.Body)
	if err != nil {
		return nil, "", err
	}

	plan := ""
	if level := strings.TrimSpace(firstString(nested(payload, "user", "membership", "level"))); level != "" {
		plan = strings.ToLower(strings.TrimPrefix(level, "LEVEL_"))
	}

	windows := make([]quota.Window, 0, 2)
	if weekly := kimiWindow("7d", "7d", mapValue(payload["usage"])); weekly != nil {
		windows = append(windows, *weekly)
	}
	if limits, ok := payload["limits"].([]any); ok {
		for _, item := range limits {
			entry := mapValue(item)
			if entry == nil {
				continue
			}
			windowInfo := mapValue(entry["window"])
			duration, _ := number(windowInfo["duration"])
			unit := strings.ToUpper(strings.TrimSpace(firstString(windowInfo["timeUnit"], windowInfo["time_unit"])))
			var id, label string
			switch {
			case duration == 300 && (unit == "TIME_UNIT_MINUTE" || unit == "MINUTE" || unit == "MINUTES"):
				id, label = "5h", "5h"
			default:
				continue
			}
			if window := kimiWindow(id, label, mapValue(entry["detail"])); window != nil {
				windows = append(windows, *window)
			}
		}
	}
	if len(windows) == 0 {
		return nil, plan, errors.New("Kimi quota response contained no usable windows")
	}
	return windows, plan, nil
}

func kimiWindow(id, label string, fields map[string]any) *quota.Window {
	if fields == nil {
		return nil
	}
	limit, limitOK := number(fields["limit"])
	if !limitOK || limit <= 0 {
		return nil
	}
	window := quota.Window{ID: id, Label: label, Source: activeQuotaSource}
	if remaining, ok := number(fields["remaining"]); ok {
		window.RemainingPercent = quota.Percent(remaining / limit * 100)
	}
	if used, ok := number(fields["used"]); ok {
		window.UsedPercent = quota.Percent(used / limit * 100)
	}
	if window.RemainingPercent == nil && window.UsedPercent == nil {
		return nil
	}
	window.ResetsAt = timeValue(fields["resetTime"], time.Time{})
	return &window
}

func (client *Client) fetchCodex(ctx context.Context, authIndex string, entry map[string]any) ([]quota.Window, string, error) {
	headers := map[string]string{
		"Authorization": "Bearer $TOKEN$",
		"Accept":        "application/json",
		"User-Agent":    "ai-quota-frame/1",
	}
	if account := accountID(entry); account != "" {
		headers["Chatgpt-Account-Id"] = account
	}
	response, err := client.apiCall(ctx, apiCallRequest{
		AuthIndex: authIndex,
		Method:    http.MethodGet,
		URL:       codexUsageURL,
		Header:    headers,
	})
	if err != nil {
		return nil, "", err
	}
	payload, err := parseJSONBody(response.Body)
	if err != nil {
		return nil, "", err
	}

	plan := strings.ToLower(strings.TrimSpace(firstString(payload["plan_type"], payload["planType"])))
	rateLimit := mapValue(firstNonNil(payload["rate_limit"], payload["rateLimit"]))
	windows := codexRateLimitWindows(rateLimit, "")
	if additional, ok := firstNonNil(payload["additional_rate_limits"], payload["additionalRateLimits"]).([]any); ok {
		for index, item := range additional {
			entry := mapValue(item)
			if entry == nil {
				continue
			}
			label := compactText(firstString(entry["limit_name"], entry["limitName"], entry["metered_feature"], entry["meteredFeature"]), 28)
			if parts := strings.Split(label, "-"); len(parts) > 2 && strings.HasPrefix(strings.ToUpper(label), "GPT") {
				label = parts[len(parts)-1]
			}
			if label == "" {
				label = fmt.Sprintf("Extra %d", index+1)
			}
			additionalRate := mapValue(firstNonNil(entry["rate_limit"], entry["rateLimit"]))
			windows = append(windows, codexRateLimitWindows(additionalRate, label+" ")...)
		}
	}
	if len(windows) == 0 {
		return nil, plan, errors.New("Codex quota response contained no usable windows")
	}
	return windows, plan, nil
}

func codexRateLimitWindows(rateLimit map[string]any, labelPrefix string) []quota.Window {
	if rateLimit == nil {
		return nil
	}
	primary := mapValue(firstNonNil(rateLimit["primary_window"], rateLimit["primaryWindow"]))
	secondary := mapValue(firstNonNil(rateLimit["secondary_window"], rateLimit["secondaryWindow"]))
	windows := make([]quota.Window, 0, 2)
	if window := codexWindow(primary, labelPrefix+"5h", "5h"); window != nil {
		windows = append(windows, *window)
	}
	if window := codexWindow(secondary, labelPrefix+"7d", "7d"); window != nil {
		windows = append(windows, *window)
	}
	return windows
}

func codexWindow(payload map[string]any, label, fallbackID string) *quota.Window {
	if payload == nil {
		return nil
	}
	rawUsed := firstNonNil(payload["used_percent"], payload["usedPercent"])
	used, hasUsed := percentage(rawUsed)
	if rawUsed != nil && !hasUsed {
		return nil
	}
	var usedPercent, remainingPercent *float64
	if hasUsed {
		usedPercent = quota.Percent(used)
		remainingPercent = quota.Percent(100 - *usedPercent)
	}
	reset := resetTime(payload, time.Now())
	if usedPercent == nil && reset == nil {
		return nil
	}
	id := fallbackID
	if seconds, ok := number(firstNonNil(payload["limit_window_seconds"], payload["limitWindowSeconds"])); ok {
		durationLabel := compactDuration(int64(seconds))
		prefix := strings.TrimSpace(strings.TrimSuffix(label, fallbackID))
		label = strings.TrimSpace(strings.TrimSpace(prefix) + " " + durationLabel)
		id = durationLabel
	}
	if label != "5h" && label != "7d" {
		id = slug(label)
	}
	return &quota.Window{
		ID:               id,
		Label:            label,
		UsedPercent:      usedPercent,
		RemainingPercent: remainingPercent,
		ResetsAt:         reset,
		Source:           activeQuotaSource,
	}
}

func compactDuration(seconds int64) string {
	switch {
	case seconds > 0 && seconds%(24*60*60) == 0:
		return fmt.Sprintf("%dd", seconds/(24*60*60))
	case seconds > 0 && seconds%(60*60) == 0:
		return fmt.Sprintf("%dh", seconds/(60*60))
	case seconds > 0 && seconds%60 == 0:
		return fmt.Sprintf("%dm", seconds/60)
	case seconds > 0:
		return fmt.Sprintf("%ds", seconds)
	default:
		return "window"
	}
}

func (client *Client) fetchClaude(ctx context.Context, authIndex string) ([]quota.Window, string, error) {
	response, err := client.apiCall(ctx, apiCallRequest{
		AuthIndex: authIndex,
		Method:    http.MethodGet,
		URL:       claudeUsageURL,
		Header: map[string]string{
			"Authorization":  "Bearer $TOKEN$",
			"Accept":         "application/json",
			"anthropic-beta": "oauth-2025-04-20",
		},
	})
	if err != nil {
		return nil, "", err
	}
	payload, err := parseJSONBody(response.Body)
	if err != nil {
		return nil, "", err
	}

	definitions := []struct {
		key   string
		id    string
		label string
	}{
		{"five_hour", "5h", "5h"},
		{"seven_day", "7d", "7d"},
		{"seven_day_oauth_apps", "oauth-apps-7d", "OAuth apps 7d"},
		{"seven_day_sonnet", "sonnet-7d", "Sonnet 7d"},
		{"seven_day_opus", "opus-7d", "Opus 7d"},
		{"seven_day_cowork", "cowork-7d", "Cowork 7d"},
		{"iguana_necktie", "iguana-necktie", "Iguana"},
	}
	fableLimit := selectedClaudeFableLimit(payload)
	windows := make([]quota.Window, 0, len(definitions)+1)
	for _, definition := range definitions {
		if definition.key == "iguana_necktie" && fableLimit != nil {
			continue
		}
		usage := mapValue(payload[definition.key])
		if usage == nil {
			continue
		}
		utilization, ok := percentage(usage["utilization"])
		if !ok {
			continue
		}
		windows = append(windows, quota.Window{
			ID:               definition.id,
			Label:            definition.label,
			UsedPercent:      quota.Percent(utilization),
			RemainingPercent: quota.Percent(100 - utilization),
			ResetsAt:         timeValue(usage["resets_at"], client.now()),
			Source:           activeQuotaSource,
		})
	}
	if fableLimit != nil {
		utilization, _ := percentage(fableLimit["percent"])
		modelName := firstString(nested(fableLimit, "scope", "model", "display_name"))
		windows = append(windows, quota.Window{
			ID:               "fable-7d",
			Label:            compactText(modelName+" 7d", 28),
			UsedPercent:      quota.Percent(utilization),
			RemainingPercent: quota.Percent(100 - utilization),
			ResetsAt:         timeValue(fableLimit["resets_at"], client.now()),
			Source:           activeQuotaSource,
		})
	}
	if extra := mapValue(payload["extra_usage"]); extra != nil {
		enabled, _ := boolean(extra["is_enabled"])
		if utilization, ok := percentage(extra["utilization"]); enabled && ok {
			windows = append(windows, quota.Window{
				ID:               "extra",
				Label:            "Extra",
				UsedPercent:      quota.Percent(utilization),
				RemainingPercent: quota.Percent(100 - utilization),
				Source:           activeQuotaSource,
			})
		}
	}
	if len(windows) == 0 {
		return nil, "", errors.New("Claude quota response contained no usable windows")
	}
	return windows, "", nil
}

func selectedClaudeFableLimit(payload map[string]any) map[string]any {
	rawLimits, ok := payload["limits"].([]any)
	if !ok {
		return nil
	}
	var fallback map[string]any
	for _, raw := range rawLimits {
		limit := mapValue(raw)
		if limit == nil || strings.ToLower(strings.TrimSpace(firstString(limit["kind"]))) != "weekly_scoped" {
			continue
		}
		modelName := strings.ToLower(strings.TrimSpace(firstString(nested(limit, "scope", "model", "display_name"))))
		if modelName != "fable" && modelName != "fable 5" {
			continue
		}
		if _, valid := percentage(limit["percent"]); !valid {
			continue
		}
		if fallback == nil {
			fallback = limit
		}
		if active, _ := boolean(limit["is_active"]); active {
			return limit
		}
	}
	return fallback
}

func (client *Client) fetchGemini(ctx context.Context, authIndex string, entry map[string]any) ([]quota.Window, string, error) {
	project := projectID(entry)
	loadedProject, plan, loadErr := client.loadGoogleProject(ctx, authIndex, project, geminiMetadata())
	if loadedProject != "" {
		project = loadedProject
	}
	if project == "" {
		if loadErr == nil {
			loadErr = errors.New("Gemini loadCodeAssist returned no project")
		}
		return nil, plan, loadErr
	}
	requestBody, _ := json.Marshal(map[string]string{"project": project})
	response, err := client.apiCall(ctx, apiCallRequest{
		AuthIndex: authIndex,
		Method:    http.MethodPost,
		URL:       geminiQuotaURL,
		Header:    googleHeaders(),
		Data:      string(requestBody),
	})
	if err != nil {
		return nil, plan, err
	}
	payload, err := parseJSONBody(response.Body)
	if err != nil {
		return nil, plan, err
	}
	windows := parseGeminiBuckets(payload)
	if len(windows) == 0 {
		return nil, plan, errors.New("Gemini quota response contained no usable buckets")
	}
	return windows, plan, nil
}

func (client *Client) fetchAntigravity(ctx context.Context, authIndex string, entry map[string]any) ([]quota.Window, string, error) {
	project := projectID(entry)
	metadata := map[string]string{
		"ideType":    "ANTIGRAVITY",
		"platform":   "PLATFORM_UNSPECIFIED",
		"pluginType": "GEMINI",
	}
	loadedProject, plan, loadErr := client.loadGoogleProjectAt(ctx, authIndex, antigravityLoadURL, project, metadata)
	if loadedProject != "" {
		project = loadedProject
	}
	if project == "" {
		if loadErr == nil {
			loadErr = errors.New("Antigravity loadCodeAssist returned no project")
		}
		return nil, plan, loadErr
	}

	requestBody, _ := json.Marshal(map[string]string{"project": project})
	var lastErr error
	for _, endpoint := range []string{antigravityDaily, antigravitySandbox, antigravityURL} {
		response, err := client.apiCall(ctx, apiCallRequest{
			AuthIndex: authIndex,
			Method:    http.MethodPost,
			URL:       endpoint,
			Header:    antigravityHeaders(),
			Data:      string(requestBody),
		})
		if err != nil {
			lastErr = err
			continue
		}
		payload, err := parseJSONBody(response.Body)
		if err != nil {
			lastErr = err
			continue
		}
		windows := parseAntigravitySummary(payload)
		if len(windows) == 0 {
			lastErr = errors.New("Antigravity quota response contained no usable buckets")
			continue
		}
		return windows, plan, nil
	}
	if lastErr == nil {
		lastErr = errors.New("Antigravity retrieveUserQuotaSummary failed")
	}
	return nil, plan, lastErr
}

func parseAntigravitySummary(payload map[string]any) []quota.Window {
	rawGroups, ok := payload["groups"].([]any)
	if !ok || len(rawGroups) == 0 {
		return nil
	}
	type aggregate struct {
		label     string
		remaining float64
		reset     *time.Time
		seen      bool
	}
	byWindow := map[string]*aggregate{}
	order := make([]string, 0)
	for _, rawGroup := range rawGroups {
		group := mapValue(rawGroup)
		if group == nil {
			continue
		}
		rawBuckets, ok := group["buckets"].([]any)
		if !ok {
			continue
		}
		for _, rawBucket := range rawBuckets {
			bucket := mapValue(rawBucket)
			if bucket == nil {
				continue
			}
			fraction, ok := fraction(firstNonNil(bucket["remainingFraction"], bucket["remaining_fraction"]))
			if !ok {
				continue
			}
			id, label := antigravityWindow(firstString(bucket["window"], bucket["displayName"], bucket["display_name"], bucket["bucketId"], bucket["bucket_id"]))
			if id == "" {
				continue
			}
			current := byWindow[id]
			if current == nil {
				current = &aggregate{label: label}
				byWindow[id] = current
				order = append(order, id)
			}
			remaining := *quota.Percent(fraction * 100)
			reset := timeValue(firstNonNil(bucket["resetTime"], bucket["reset_time"]), time.Now())
			if !current.seen || remaining < current.remaining {
				current.remaining = remaining
				current.reset = reset
			}
			current.seen = true
		}
	}
	preferred := []string{"5h", "7d"}
	orderedIDs := make([]string, 0, len(order))
	seen := map[string]bool{}
	for _, id := range append(preferred, order...) {
		if byWindow[id] == nil || seen[id] {
			continue
		}
		seen[id] = true
		orderedIDs = append(orderedIDs, id)
	}
	result := make([]quota.Window, 0, len(orderedIDs))
	for _, id := range orderedIDs {
		window := byWindow[id]
		result = append(result, quota.Window{
			ID:               id,
			Label:            window.label,
			UsedPercent:      quota.Percent(100 - window.remaining),
			RemainingPercent: quota.Percent(window.remaining),
			ResetsAt:         window.reset,
			Source:           activeQuotaSource,
		})
	}
	return result
}

func antigravityWindow(value string) (string, string) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "5h", "five-hour", "five_hour":
		return "5h", "5h"
	case "weekly", "week", "7d", "seven-day", "seven_day":
		return "7d", "7d"
	default:
		id := slug(value)
		if id == "" {
			return "", ""
		}
		return id, compactText(value, 24)
	}
}

func (client *Client) loadGoogleProject(ctx context.Context, authIndex, project string, metadata map[string]string) (string, string, error) {
	return client.loadGoogleProjectAt(ctx, authIndex, geminiLoadURL, project, metadata)
}

func (client *Client) loadGoogleProjectAt(ctx context.Context, authIndex, endpoint, project string, metadata map[string]string) (string, string, error) {
	if project != "" {
		metadata["duetProject"] = project
	}
	requestPayload := map[string]any{"metadata": metadata}
	if project != "" {
		requestPayload["cloudaicompanionProject"] = project
	}
	requestBody, _ := json.Marshal(requestPayload)
	response, err := client.apiCall(ctx, apiCallRequest{
		AuthIndex: authIndex,
		Method:    http.MethodPost,
		URL:       endpoint,
		Header:    googleHeaders(),
		Data:      string(requestBody),
	})
	if err != nil {
		return "", "", err
	}
	payload, err := parseJSONBody(response.Body)
	if err != nil {
		return "", "", err
	}
	loadedProject := firstString(payload["cloudaicompanionProject"])
	if loadedProject == "" {
		loadedProject = firstString(nested(payload, "cloudaicompanionProject", "id"))
	}
	if loadedProject == "" {
		loadedProject = project
	}
	if loadedProject == "" {
		return "", "", errors.New("Gemini loadCodeAssist returned no project")
	}
	plan := strings.ToLower(strings.TrimSpace(firstString(
		nested(payload, "paidTier", "id"),
		nested(payload, "paidTier", "name"),
		nested(payload, "currentTier", "id"),
		nested(payload, "currentTier", "name"),
	)))
	return loadedProject, plan, nil
}

func antigravityHeaders() map[string]string {
	return map[string]string{
		"Authorization": "Bearer $TOKEN$",
		"Content-Type":  "application/json",
		"User-Agent":    "antigravity/cli/1.0.13 (aidev_client; os_type=linux; arch=amd64)",
	}
}

func googleHeaders() map[string]string {
	return map[string]string{
		"Authorization": "Bearer $TOKEN$",
		"Content-Type":  "application/json",
		"User-Agent":    "ai-quota-frame/1",
	}
}

func geminiMetadata() map[string]string {
	return map[string]string{
		"ideType":    "IDE_UNSPECIFIED",
		"platform":   "PLATFORM_UNSPECIFIED",
		"pluginType": "GEMINI",
	}
}

func parseGeminiBuckets(payload map[string]any) []quota.Window {
	rawBuckets, ok := payload["buckets"].([]any)
	if !ok {
		return nil
	}
	type aggregate struct {
		label     string
		remaining float64
		reset     *time.Time
		seen      bool
	}
	groups := map[string]*aggregate{
		"pro":   {label: "Pro"},
		"flash": {label: "Flash"},
		"other": {label: "Other"},
	}
	for _, item := range rawBuckets {
		bucket := mapValue(item)
		if bucket == nil {
			continue
		}
		modelID := strings.ToLower(firstString(bucket["modelId"], bucket["model_id"]))
		remainingFraction, ok := fraction(firstNonNil(bucket["remainingFraction"], bucket["remaining_fraction"], bucket["remaining"]))
		if !ok {
			continue
		}
		groupID := "other"
		switch {
		case strings.Contains(modelID, "pro"):
			groupID = "pro"
		case strings.Contains(modelID, "flash"):
			groupID = "flash"
		}
		current := groups[groupID]
		remaining := *quota.Percent(remainingFraction * 100)
		reset := timeValue(firstNonNil(bucket["resetTime"], bucket["reset_time"]), time.Now())
		if !current.seen || remaining < current.remaining {
			current.remaining = remaining
			current.reset = reset
		}
		current.seen = true
	}

	order := []string{"pro", "flash", "other"}
	windows := make([]quota.Window, 0, len(order))
	for _, id := range order {
		group := groups[id]
		if !group.seen {
			continue
		}
		windows = append(windows, quota.Window{
			ID:               id,
			Label:            group.label,
			UsedPercent:      quota.Percent(100 - group.remaining),
			RemainingPercent: quota.Percent(group.remaining),
			ResetsAt:         group.reset,
			Source:           activeQuotaSource,
		})
	}
	sort.SliceStable(windows, func(i, j int) bool { return geminiWindowRank(windows[i].ID) < geminiWindowRank(windows[j].ID) })
	return windows
}

func geminiWindowRank(id string) int {
	switch id {
	case "pro":
		return 0
	case "flash":
		return 1
	default:
		return 2
	}
}

func resetTime(payload map[string]any, now time.Time) *time.Time {
	if reset := timeValue(firstNonNil(payload["reset_at"], payload["resetAt"]), now); reset != nil {
		return reset
	}
	if seconds, ok := number(firstNonNil(payload["reset_after_seconds"], payload["resetAfterSeconds"])); ok && seconds >= 0 {
		reset := now.Add(time.Duration(seconds * float64(time.Second))).UTC()
		return &reset
	}
	return nil
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func slug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	lastDash := false
	for _, current := range value {
		if current >= 'a' && current <= 'z' || current >= '0' && current <= '9' {
			builder.WriteRune(current)
			lastDash = false
		} else if !lastDash && builder.Len() > 0 {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}
