package dashboard

import (
	"fmt"
	"strings"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/quota"
)

type displayWindow struct {
	label     string
	remaining *float64
	reset     *time.Time
}

type displayRow struct {
	group    string
	provider string
	detail   string
	status   string
	stale    bool
	windows  []displayWindow
}

func snapshotUsesSource(snapshot quota.Snapshot, source string) bool {
	for _, account := range snapshot.Accounts {
		for _, window := range account.Windows {
			if window.Source == source {
				return true
			}
		}
	}
	return snapshot.Usage != nil && snapshot.Usage.Source == source
}

func aggregateRows(accounts []quota.Account, providers []DisplayProvider) []displayRow {
	if len(providers) == 0 {
		providers = DefaultDisplayProviders()
	}
	rows := make([]displayRow, 0, len(providers))
	for _, provider := range providers {
		var matching []quota.Account
		for _, account := range accounts {
			if containsString(provider.IDs, account.Provider) {
				matching = append(matching, account)
			}
		}
		if len(matching) == 0 {
			rows = append(rows, displayRow{group: provider.Group, provider: provider.Label, detail: "No OAuth account", status: "missing"})
			continue
		}
		effective := matching
		enabled := make([]quota.Account, 0, len(matching))
		for _, account := range matching {
			if account.Status != "disabled" {
				enabled = append(enabled, account)
			}
		}
		if len(enabled) > 0 {
			effective = enabled
		}

		status := "ok"
		hasStale := false
		plans := map[string]struct{}{}
		for _, account := range effective {
			if statusRank(account.Status) > statusRank(status) {
				status = account.Status
			}
			if account.Plan != "" {
				plans[account.Plan] = struct{}{}
			}
			if account.Stale || account.Warning != "" {
				hasStale = true
			}
		}
		detail := fmt.Sprintf("%d account", len(effective))
		if len(effective) != 1 {
			detail += "s"
		}
		if len(effective) != len(matching) {
			detail = fmt.Sprintf("%d active / %d total", len(effective), len(matching))
		}
		if len(plans) == 1 {
			for plan := range plans {
				detail += " / " + plan
			}
		}
		rows = append(rows, displayRow{
			group:    provider.Group,
			provider: provider.Label,
			detail:   detail,
			status:   status,
			stale:    hasStale,
			windows:  aggregateWindows(provider.Group, effective),
		})
	}
	return rows
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func aggregateWindows(provider string, accounts []quota.Account) []displayWindow {
	type aggregate struct {
		label     string
		remaining *float64
		reset     *time.Time
	}
	byID := map[string]*aggregate{}
	order := []string{}
	for _, account := range accounts {
		for _, window := range account.Windows {
			current := byID[window.ID]
			if current == nil {
				current = &aggregate{label: window.Label}
				byID[window.ID] = current
				order = append(order, window.ID)
			}
			if window.RemainingPercent != nil && (current.remaining == nil || *window.RemainingPercent < *current.remaining) {
				value := *window.RemainingPercent
				current.remaining = &value
				current.reset = window.ResetsAt
			}
		}
	}
	var preferred []string
	switch provider {
	case "codex", "kimi", "claude":
		preferred = []string{"5h", "7d"}
	}
	ordered := []string{}
	seen := map[string]bool{}
	for _, id := range append(preferred, order...) {
		if byID[id] != nil && !seen[id] {
			seen[id] = true
			ordered = append(ordered, id)
		}
	}
	if len(ordered) > 2 {
		ordered = ordered[:2]
	}
	result := make([]displayWindow, 0, len(ordered))
	for _, id := range ordered {
		current := byID[id]
		result = append(result, displayWindow{label: current.label, remaining: current.remaining, reset: current.reset})
	}
	return result
}

func statusRank(status string) int {
	switch status {
	case "error":
		return 5
	case "exhausted":
		return 4
	case "low":
		return 3
	case "disabled":
		return 2
	case "unknown":
		return 1
	default:
		return 0
	}
}

func formatTokens(value int64) string {
	switch {
	case value >= 1_000_000_000:
		return fmt.Sprintf("%.2fB", float64(value)/1e9)
	case value >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(value)/1e6)
	case value >= 1_000:
		return fmt.Sprintf("%.1fk", float64(value)/1e3)
	default:
		return fmt.Sprintf("%d", value)
	}
}

func clamp(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func compact(value string, maxRunes int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= maxRunes {
		return string(runes)
	}
	if maxRunes <= 3 {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-3]) + "..."
}
