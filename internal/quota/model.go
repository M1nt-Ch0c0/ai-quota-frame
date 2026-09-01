package quota

import (
	"math"
	"time"
)

const SchemaVersion = 1

type Window struct {
	ID               string     `json:"id"`
	Label            string     `json:"label"`
	UsedPercent      *float64   `json:"used_percent,omitempty"`
	RemainingPercent *float64   `json:"remaining_percent,omitempty"`
	ResetsAt         *time.Time `json:"resets_at,omitempty"`
	ObservedAt       *time.Time `json:"observed_at,omitempty"`
	Source           string     `json:"source"`
}

type Account struct {
	RetentionID string     `json:"-"`
	Provider    string     `json:"provider"`
	Name        string     `json:"name"`
	Plan        string     `json:"plan,omitempty"`
	Status      string     `json:"status"`
	Stale       bool       `json:"stale,omitempty"`
	LastFreshAt *time.Time `json:"last_fresh_at,omitempty"`
	Windows     []Window   `json:"windows"`
	Warning     string     `json:"warning,omitempty"`
	Error       string     `json:"error,omitempty"`
}

// UsageDay is one calendar day's token consumption and estimated API cost.
type UsageDay struct {
	Date   string  `json:"date"`
	Tokens int64   `json:"tokens"`
	Cost   float64 `json:"cost"`
}

// Usage summarizes token consumption and estimated cost across all
// proxied providers, as reported by the local usage collector.
type Usage struct {
	TodayTokens int64      `json:"today_tokens"`
	TodayCost   float64    `json:"today_cost"`
	Currency    string     `json:"currency"`
	Source      string     `json:"source"`
	Days        []UsageDay `json:"days,omitempty"`
}

type Snapshot struct {
	SchemaVersion int        `json:"schema_version"`
	RefreshedAt   time.Time  `json:"refreshed_at"`
	DataUpdatedAt time.Time  `json:"data_updated_at"`
	LastFreshAt   *time.Time `json:"last_fresh_at,omitempty"`
	NextRefreshAt time.Time  `json:"next_refresh_at"`
	Stale         bool       `json:"stale"`
	Accounts      []Account  `json:"accounts"`
	Usage         *Usage     `json:"usage,omitempty"`
	Errors        []string   `json:"errors,omitempty"`
}

func Percent(value float64) *float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	if value < 0 {
		value = 0
	}
	if value > 100 {
		value = 100
	}
	return &value
}

func StatusForWindows(windows []Window) string {
	minimum := 101.0
	found := false
	for _, window := range windows {
		if window.RemainingPercent == nil {
			continue
		}
		found = true
		if *window.RemainingPercent < minimum {
			minimum = *window.RemainingPercent
		}
	}
	if !found {
		return "unknown"
	}
	switch {
	case minimum <= 0:
		return "exhausted"
	case minimum <= 20:
		return "low"
	default:
		return "ok"
	}
}
