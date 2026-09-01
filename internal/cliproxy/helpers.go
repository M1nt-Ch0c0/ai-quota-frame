package cliproxy

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

func rawProviderOf(entry map[string]any) string {
	return strings.ToLower(strings.TrimSpace(firstString(entry["provider"], entry["type"])))
}

func providerOf(entry map[string]any) string {
	provider := rawProviderOf(entry)
	switch provider {
	case "gemini", "gemini_cli", "gemini-cli-oauth":
		return "gemini-cli"
	case "anti-gravity":
		return "antigravity"
	case "anthropic":
		return "claude"
	default:
		return provider
	}
}

func authKindOf(entry map[string]any) string {
	return strings.ToLower(strings.TrimSpace(firstString(entry["account_type"])))
}

func planOf(entry map[string]any) string {
	return strings.ToLower(strings.TrimSpace(firstString(
		entry["plan_type"],
		entry["planType"],
		nested(entry, "metadata", "plan_type"),
		nested(entry, "attributes", "plan_type"),
	)))
}

func percentage(value any) (float64, bool) {
	parsed, ok := number(value)
	return parsed, ok && parsed >= 0 && parsed <= 100
}

func fraction(value any) (float64, bool) {
	parsed, ok := number(value)
	return parsed, ok && parsed >= 0 && parsed <= 1
}

func displayName(entry map[string]any) string {
	if label := strings.TrimSpace(firstString(entry["label"], entry["note"])); label != "" {
		return compactText(label, 36)
	}
	if email := strings.TrimSpace(firstString(entry["email"], nested(entry, "metadata", "email"))); email != "" {
		return maskEmail(email)
	}
	provider := providerOf(entry)
	if provider == "gemini-cli" {
		provider = "gemini"
	}
	return provider + " account"
}

func maskEmail(email string) string {
	parts := strings.SplitN(strings.TrimSpace(email), "@", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "account"
	}
	local := []rune(parts[0])
	return string(local[0]) + "***@" + parts[1]
}

func boolAt(entry map[string]any, key string) bool {
	value, ok := entry[key]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		parsed, _ := strconv.ParseBool(strings.TrimSpace(typed))
		return parsed
	default:
		return false
	}
}

func stringAt(entry map[string]any, key string) string {
	return strings.TrimSpace(firstString(entry[key]))
}

func firstString(values ...any) string {
	for _, value := range values {
		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) != "" {
				return typed
			}
		case json.Number:
			return typed.String()
		case float64:
			return strconv.FormatFloat(typed, 'f', -1, 64)
		case int:
			return strconv.Itoa(typed)
		case int64:
			return strconv.FormatInt(typed, 10)
		}
	}
	return ""
}

func nested(value any, keys ...string) any {
	current := value
	for _, key := range keys {
		mapping, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = mapping[key]
	}
	return current
}

func mapValue(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return typed
	case string:
		var output map[string]any
		if json.Unmarshal([]byte(typed), &output) == nil {
			return output
		}
	}
	return nil
}

func parseJSONBody(body string) (map[string]any, error) {
	var payload map[string]any
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("provider quota response was invalid JSON: %w", err)
	}
	return payload, nil
}

func number(value any) (float64, bool) {
	finite := func(value float64) (float64, bool) {
		return value, !math.IsNaN(value) && !math.IsInf(value, 0)
	}
	switch typed := value.(type) {
	case float64:
		return finite(typed)
	case float32:
		return finite(float64(typed))
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, false
		}
		return finite(parsed)
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return 0, false
		}
		return finite(parsed)
	default:
		return 0, false
	}
}

func boolean(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		return parsed, err == nil
	default:
		return false, false
	}
}

func timeValue(value any, now time.Time) *time.Time {
	if value == nil {
		return nil
	}
	if numeric, ok := number(value); ok && numeric > 0 {
		parsed := time.Unix(int64(numeric), 0).UTC()
		return &parsed
	}
	text := strings.TrimSpace(firstString(value))
	if text == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, text); err == nil {
			parsed = parsed.UTC()
			return &parsed
		}
	}
	_ = now
	return nil
}

func accountID(entry map[string]any) string {
	for _, candidate := range []any{entry["id_token"], nested(entry, "metadata", "id_token"), nested(entry, "attributes", "id_token")} {
		claims := jwtClaims(candidate)
		if claims == nil {
			continue
		}
		if account := firstString(claims["chatgpt_account_id"]); account != "" {
			return account
		}
		if account := firstString(nested(claims, "https://api.openai.com/auth", "chatgpt_account_id")); account != "" {
			return account
		}
	}
	return ""
}

func jwtClaims(value any) map[string]any {
	if mapped := mapValue(value); mapped != nil {
		return mapped
	}
	text := strings.TrimSpace(firstString(value))
	parts := strings.Split(text, ".")
	if len(parts) < 2 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims map[string]any
	if json.Unmarshal(payload, &claims) != nil {
		return nil
	}
	return claims
}

func projectID(entry map[string]any) string {
	for _, value := range []any{entry["project_id"], nested(entry, "metadata", "project_id"), nested(entry, "attributes", "project_id")} {
		if project := strings.TrimSpace(firstString(value)); project != "" {
			return project
		}
	}
	account := strings.TrimSpace(firstString(entry["account"]))
	if start := strings.LastIndex(account, "("); start >= 0 && strings.HasSuffix(account, ")") {
		if project := strings.TrimSpace(account[start+1 : len(account)-1]); project != "" {
			return project
		}
	}
	return ""
}
