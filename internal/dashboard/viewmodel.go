package dashboard

import (
	"fmt"
	"html/template"
	"strings"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/quota"
)

type frameData struct {
	StatusText  string
	StatusClass string
	UpdatedText string
	RowCount    int
	Rows        []rowData
	Usage       *usageData
	Footer      string
	FooterClass string
}

type rowData struct {
	Logo        template.HTML
	Brand       string
	Slot        int
	Provider    string
	Detail      string
	Plan        string
	Status      string
	StatusClass string
	Stale       bool
	Windows     []windowData
	ResetCards  []string
	MoreCards   int
	CardCount   int
	// CardsPanel moves the cards into the free window column when a row
	// has a single quota window, e.g. Codex Pro with no 5h limit.
	CardsPanel bool
}

type windowData struct {
	Label        string
	PercentText  string
	PercentClass string
	ResetText    string
	Segments     []barSegmentData
}

type barSegmentData struct {
	FillClass string
	FillWidth int
}

type usageData struct {
	Days     []usageDayData
	Today    usageMetricData
	SevenDay usageMetricData
	Peak     usageMetricData
}

type usageDayData struct {
	Peak   bool
	Label  string
	Value  string
	Height int
	Title  string
}

type usageMetricData struct {
	Label  string
	Note   string
	Tokens string
	Cost   string
}

func buildFrameData(snapshot quota.Snapshot, location *time.Location, providers []DisplayProvider) frameData {
	if location == nil {
		location = time.UTC
	}

	statusText := ""
	statusClass := "live"
	if snapshot.Stale {
		statusText = "STALE"
		statusClass = "stale"
	} else if snapshotUsesSource(snapshot, "demo") {
		statusText = "DEMO"
		statusClass = "demo"
	}

	updatedText := "Waiting for first refresh"
	if !snapshot.DataUpdatedAt.IsZero() {
		updatedText = "Data " + snapshot.DataUpdatedAt.In(location).Format("01-02 15:04")
	}

	aggregated := aggregateRows(snapshot.Accounts, providers)
	rows := make([]rowData, 0, len(aggregated))
	for _, row := range aggregated {
		windows := make([]windowData, 0, len(row.windows))
		for _, window := range row.windows {
			windows = append(windows, buildWindowData(window, location))
		}
		cards, moreCards := buildResetCards(row.cards, location)
		rows = append(rows, rowData{
			Logo:        ProviderLogoSVG(row.group),
			Brand:       brandClass(row.group),
			Slot:        len(rows) + 1,
			Provider:    row.provider,
			Detail:      row.detail,
			Plan:        row.plan,
			Status:      strings.ToUpper(row.status),
			StatusClass: rowStatusClass(row),
			Stale:       row.stale,
			Windows:     windows,
			ResetCards:  cards,
			MoreCards:   moreCards,
			CardCount:   len(row.cards),
			CardsPanel:  len(cards) > 0 && len(windows) == 1 && len(aggregated) <= 3,
		})
	}

	data := frameData{
		StatusText:  statusText,
		StatusClass: statusClass,
		UpdatedText: updatedText,
		RowCount:    len(rows),
		Rows:        rows,
		Usage:       buildUsageData(snapshot.Usage),
		Footer:      "Updates on change",
		FooterClass: "muted",
	}
	if errors := displayedErrors(snapshot, providers); len(errors) > 0 {
		data.Footer = compact(errors[0], 34)
		data.FooterClass = "error"
	}
	return data
}

// displayedErrors drops "provider: message" errors for account providers that
// have no row on the frame. Errors without a provider prefix are kept.
func displayedErrors(snapshot quota.Snapshot, providers []DisplayProvider) []string {
	if len(providers) == 0 {
		providers = DefaultDisplayProviders()
	}
	displayed := map[string]bool{}
	for _, provider := range providers {
		for _, id := range provider.IDs {
			displayed[id] = true
		}
	}
	accountProviders := map[string]bool{}
	for _, account := range snapshot.Accounts {
		accountProviders[account.Provider] = true
	}
	result := make([]string, 0, len(snapshot.Errors))
	for _, message := range snapshot.Errors {
		if provider, _, ok := strings.Cut(message, ": "); ok && accountProviders[provider] && !displayed[provider] {
			continue
		}
		result = append(result, message)
	}
	return result
}

// brandClass picks the dithered tint behind a provider logo.
func brandClass(group string) string {
	switch group {
	case "claude":
		return "claude"
	case "codex":
		return "codex"
	case "xai":
		return "grok"
	case "kimi":
		return "kimi"
	case "gemini-cli", "antigravity":
		return "gemini"
	default:
		return "other"
	}
}

func rowStatusClass(row displayRow) string {
	switch {
	case row.status == "low" || row.status == "exhausted" || row.status == "error":
		return "urgent"
	case row.status == "disabled" || row.status == "missing" || row.status == "unknown" || row.stale:
		return "warn"
	default:
		return "ok"
	}
}

func buildWindowData(window displayWindow, location *time.Location) windowData {
	data := windowData{
		Label:        compact(window.label, 18),
		PercentText:  "--%",
		PercentClass: "muted",
		ResetText:    "Reset --",
		Segments:     buildBarSegments(window.remaining),
	}
	if window.remaining != nil {
		remaining := clamp(*window.remaining)
		data.PercentText = fmt.Sprintf("%.0f%%", remaining)
		switch {
		case remaining < 10:
			data.PercentClass = "urgent"
		case remaining < 40:
			data.PercentClass = "warn"
		default:
			data.PercentClass = "ok"
		}
	}
	if window.reset != nil {
		data.ResetText = "Reset " + window.reset.In(location).Format("01-02 15:04")
	}
	return data
}

const maxResetCards = 3

// buildResetCards returns the expiry dates of the soonest-expiring cards and
// how many more cards are not shown.
func buildResetCards(cards []quota.ResetCard, location *time.Location) ([]string, int) {
	if len(cards) == 0 {
		return nil, 0
	}
	shown := cards
	if len(shown) > maxResetCards {
		shown = shown[:maxResetCards]
	}
	result := make([]string, 0, len(shown))
	for _, card := range shown {
		text := "--"
		if card.ExpiresAt != nil {
			text = card.ExpiresAt.In(location).Format("01-02")
		}
		result = append(result, text)
	}
	return result, len(cards) - len(shown)
}

func buildBarSegments(remaining *float64) []barSegmentData {
	segments := make([]barSegmentData, 10)
	value := 0.0
	if remaining != nil {
		value = clamp(*remaining)
	}
	for index := range segments {
		class := "ok"
		switch {
		case index == 0:
			class = "urgent"
		case index < 4:
			class = "warn"
		}

		lower := float64(index * 10)
		fill := int((value-lower)*10 + 0.5)
		if fill < 0 {
			fill = 0
		}
		if fill > 100 {
			fill = 100
		}
		segments[index] = barSegmentData{FillClass: class, FillWidth: fill}
	}
	return segments
}

func buildUsageData(usage *quota.Usage) *usageData {
	if usage == nil || len(usage.Days) == 0 {
		return nil
	}
	currency := usage.Currency
	if currency == "" {
		currency = "$"
	}
	days := usage.Days
	if len(days) > 7 {
		days = days[len(days)-7:]
	}

	var peak quota.UsageDay
	var maxTokens int64
	for _, day := range days {
		if day.Tokens > maxTokens {
			maxTokens = day.Tokens
			peak = day
		}
	}

	chartDays := make([]usageDayData, 0, len(days))
	for _, day := range days {
		chartDays = append(chartDays, usageDayData{
			Peak:   maxTokens > 0 && day.Tokens == maxTokens,
			Label:  formatUsageDate(day.Date),
			Value:  formatTokens(day.Tokens),
			Height: usageBarHeight(day.Tokens, maxTokens),
			Title:  formatTokens(day.Tokens) + " tok",
		})
	}

	return &usageData{
		Days: chartDays,
		Today: usageMetricData{
			Label:  "TODAY",
			Tokens: formatTokens(usage.TodayTokens),
			Cost:   fmt.Sprintf("%s%.2f", currency, usage.TodayCost),
		},
		SevenDay: usageMetricData{
			Label:  "7DAY",
			Tokens: formatTokens(sumTokens(days)),
			Cost:   fmt.Sprintf("%s%.2f", currency, sumCost(days)),
		},
		Peak: usageMetricData{
			Label:  "PEAK",
			Note:   formatUsageDate(peak.Date),
			Tokens: formatTokens(peak.Tokens),
			Cost:   fmt.Sprintf("%s%.2f", currency, peak.Cost),
		},
	}
}

func formatUsageDate(value string) string {
	if parsed, err := time.Parse("2006-01-02", value); err == nil {
		return parsed.Format("01-02")
	}
	if len(value) > 5 {
		return value[len(value)-5:]
	}
	return value
}

func usageBarHeight(value, maximum int64) int {
	if value <= 0 || maximum <= 0 {
		return 0
	}
	height := int(float64(value) / float64(maximum) * 100)
	if height < 4 {
		return 4
	}
	if height > 100 {
		return 100
	}
	return height
}

func sumTokens(days []quota.UsageDay) int64 {
	var total int64
	for _, day := range days {
		total += day.Tokens
	}
	return total
}

func sumCost(days []quota.UsageDay) float64 {
	var total float64
	for _, day := range days {
		total += day.Cost
	}
	return total
}
