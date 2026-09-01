package cliproxy

import (
	"strings"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/quota"
)

func passiveWindows(provider string, entry map[string]any, now time.Time, maxAge time.Duration) []quota.Window {
	signals, observedAt, hasObservedAt := newestSignalSnapshot(entry)
	if len(signals) == 0 || !hasObservedAt || maxAge <= 0 {
		return nil
	}
	age := now.Sub(observedAt)
	if age > maxAge || age < -5*time.Minute {
		return nil
	}
	switch provider {
	case "codex":
		return passiveCodexWindows(signals, observedAt)
	case "claude":
		return passiveClaudeWindows(signals, observedAt)
	default:
		return nil
	}
}

func newestSignalSnapshot(entry map[string]any) (map[string]string, time.Time, bool) {
	bestSignals := stringMap(nested(entry, "quota", "signals"))
	bestObserved, bestHasObserved := observedTime(nested(entry, "quota", "observed_at"))
	if !bestHasObserved {
		bestSignals = nil
	}
	if modelQuotas := mapValue(entry["model_quotas"]); modelQuotas != nil {
		for _, raw := range modelQuotas {
			candidate := mapValue(raw)
			if candidate == nil {
				continue
			}
			candidateSignals := stringMap(candidate["signals"])
			candidateObserved, candidateHasObserved := observedTime(candidate["observed_at"])
			if candidateHasObserved && len(candidateSignals) > 0 && (!bestHasObserved || candidateObserved.After(bestObserved)) {
				bestSignals = candidateSignals
				bestObserved = candidateObserved
				bestHasObserved = true
			}
		}
	}
	return bestSignals, bestObserved, bestHasObserved
}

func passiveCodexWindows(signals map[string]string, observedAt time.Time) []quota.Window {
	definitions := []struct {
		prefix string
		id     string
		label  string
	}{
		{"X-Codex-Primary-", "5h", "5h"},
		{"X-Codex-Secondary-", "7d", "7d"},
	}
	windows := make([]quota.Window, 0, 2)
	for _, definition := range definitions {
		used, ok := percentage(signal(signals, definition.prefix+"Used-Percent"))
		if !ok {
			continue
		}
		windows = append(windows, quota.Window{
			ID:               definition.id,
			Label:            definition.label,
			UsedPercent:      quota.Percent(used),
			RemainingPercent: quota.Percent(100 - used),
			ResetsAt:         signalReset(signals, definition.prefix, observedAt),
			ObservedAt:       timePointer(observedAt),
			Source:           passiveQuotaSource,
		})
	}
	return windows
}

func passiveClaudeWindows(signals map[string]string, observedAt time.Time) []quota.Window {
	definitions := []struct {
		prefix string
		id     string
		label  string
	}{
		{"Anthropic-Ratelimit-Unified-5h-", "5h", "5h"},
		{"Anthropic-Ratelimit-Unified-7d-", "7d", "7d"},
	}
	windows := make([]quota.Window, 0, 2)
	for _, definition := range definitions {
		utilization, ok := fraction(signal(signals, definition.prefix+"Utilization"))
		if !ok {
			continue
		}
		used := utilization * 100
		windows = append(windows, quota.Window{
			ID:               definition.id,
			Label:            definition.label,
			UsedPercent:      quota.Percent(used),
			RemainingPercent: quota.Percent(100 - used),
			ResetsAt:         signalReset(signals, definition.prefix, observedAt),
			ObservedAt:       timePointer(observedAt),
			Source:           passiveQuotaSource,
		})
	}
	return windows
}

func timePointer(value time.Time) *time.Time {
	value = value.UTC()
	return &value
}

func signalReset(signals map[string]string, prefix string, observedAt time.Time) *time.Time {
	if value := signal(signals, prefix+"Reset-At"); value != "" {
		return timeValue(value, observedAt)
	}
	if value := signal(signals, prefix+"Reset"); value != "" {
		return timeValue(value, observedAt)
	}
	if seconds, ok := number(signal(signals, prefix+"Reset-After-Seconds")); ok && seconds >= 0 {
		reset := observedAt.Add(time.Duration(seconds * float64(time.Second))).UTC()
		return &reset
	}
	return nil
}

func signal(signals map[string]string, name string) string {
	for key, value := range signals {
		if strings.EqualFold(strings.TrimSpace(key), name) {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func stringMap(value any) map[string]string {
	output := map[string]string{}
	for key, raw := range mapValue(value) {
		if text := firstString(raw); text != "" {
			output[key] = text
		}
	}
	return output
}

func observedTime(value any) (time.Time, bool) {
	if parsed := timeValue(value, time.Time{}); parsed != nil {
		return *parsed, true
	}
	return time.Time{}, false
}
