package dashboard

import (
	"fmt"
	"strings"
)

const maxDisplayProviders = 5

// DisplayProvider is one OAuth subscription row on the 800x480 frame.
type DisplayProvider struct {
	IDs   []string
	Group string
	Label string
}

func DefaultDisplayProviders() []DisplayProvider {
	return []DisplayProvider{
		{IDs: []string{"codex"}, Group: "codex", Label: "CODEX"},
		{IDs: []string{"xai"}, Group: "xai", Label: "GROK"},
		{IDs: []string{"kimi"}, Group: "kimi", Label: "KIMI"},
	}
}

// ParseDisplayProviders reads DISPLAY_PROVIDERS.
// Empty input returns the default three rows.
// Each comma-separated slot is `id[+id...][:LABEL]`.
// Example: `codex,claude:CLAUDE,gemini-cli+antigravity:GEMINI`.
func ParseDisplayProviders(raw string) ([]DisplayProvider, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return DefaultDisplayProviders(), nil
	}
	slots := strings.Split(trimmed, ",")
	providers := make([]DisplayProvider, 0, len(slots))
	seenGroup := map[string]bool{}
	for _, slot := range slots {
		slot = strings.TrimSpace(slot)
		if slot == "" {
			continue
		}
		idPart, label := slot, ""
		if index := strings.LastIndex(slot, ":"); index >= 0 {
			idPart = strings.TrimSpace(slot[:index])
			label = strings.TrimSpace(slot[index+1:])
		}
		if idPart == "" {
			return nil, fmt.Errorf("DISPLAY_PROVIDERS slot %q has no provider id", slot)
		}
		ids := make([]string, 0, 2)
		for _, rawID := range strings.Split(idPart, "+") {
			normalized, extra, err := normalizeProviderID(rawID)
			if err != nil {
				return nil, err
			}
			ids = append(ids, normalized)
			ids = append(ids, extra...)
		}
		ids = uniqueStrings(ids)
		if len(ids) == 0 {
			return nil, fmt.Errorf("DISPLAY_PROVIDERS slot %q has no provider id", slot)
		}
		group := ids[0]
		if seenGroup[group] {
			return nil, fmt.Errorf("DISPLAY_PROVIDERS repeats provider %q", group)
		}
		seenGroup[group] = true
		if label == "" {
			label = defaultProviderLabel(ids)
		}
		label = strings.ToUpper(compact(label, 12))
		if label == "" {
			return nil, fmt.Errorf("DISPLAY_PROVIDERS slot %q has an empty label", slot)
		}
		providers = append(providers, DisplayProvider{IDs: ids, Group: group, Label: label})
	}
	if len(providers) == 0 {
		return nil, fmt.Errorf("DISPLAY_PROVIDERS must list at least one provider")
	}
	if len(providers) > maxDisplayProviders {
		return nil, fmt.Errorf("DISPLAY_PROVIDERS supports at most %d rows", maxDisplayProviders)
	}
	return providers, nil
}

func normalizeProviderID(raw string) (string, []string, error) {
	id := strings.ToLower(strings.TrimSpace(raw))
	id = strings.ReplaceAll(id, "_", "-")
	switch id {
	case "":
		return "", nil, fmt.Errorf("DISPLAY_PROVIDERS contains an empty provider id")
	case "codex":
		return "codex", nil, nil
	case "claude", "anthropic":
		return "claude", nil, nil
	case "gemini", "gemini-cli", "gemini-cli-oauth":
		return "gemini-cli", nil, nil
	case "antigravity", "anti-gravity":
		return "antigravity", nil, nil
	case "google":
		return "gemini-cli", []string{"antigravity"}, nil
	case "xai", "grok":
		return "xai", nil, nil
	case "kimi":
		return "kimi", nil, nil
	default:
		if strings.ContainsAny(id, " \t") {
			return "", nil, fmt.Errorf("DISPLAY_PROVIDERS provider id %q is invalid", raw)
		}
		return id, nil, nil
	}
}

func defaultProviderLabel(ids []string) string {
	if len(ids) == 0 {
		return "OAUTH"
	}
	if containsString(ids, "gemini-cli") && containsString(ids, "antigravity") {
		return "GEMINI"
	}
	switch ids[0] {
	case "codex":
		return "CODEX"
	case "claude":
		return "CLAUDE"
	case "gemini-cli":
		return "GEMINI"
	case "antigravity":
		return "ANTIGRAVITY"
	case "xai":
		return "GROK"
	case "kimi":
		return "KIMI"
	default:
		return strings.ToUpper(ids[0])
	}
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}
