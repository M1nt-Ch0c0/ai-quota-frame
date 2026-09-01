package dashboard

import (
	"fmt"
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
	Chart       *chartData
	Usage       *usageData
	Footer      string
	FooterClass string
}

type rowData struct {
	Provider    string
	Detail      string
	Status      string
	StatusClass string
	Stale       bool
	Windows     []windowData
}

type windowData struct {
	Label        string
	PercentText  string
	PercentClass string
	BarWidth     int
	ResetText    string
}

type usageData struct {
	Tokens string
	Cost   string
}

type chartData struct {
	Title string
	Days  []chartDay
}

type chartDay struct {
	Label       string
	TokenHeight int
	CostHeight  int
	TokenTitle  string
	CostTitle   string
}

func buildFrameData(snapshot quota.Snapshot, location *time.Location, providers []DisplayProvider) frameData {
	if location == nil {
		location = time.UTC
	}

	statusText := "LIVE"
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
		rows = append(rows, rowData{
			Provider:    row.provider,
			Detail:      row.detail,
			Status:      strings.ToUpper(row.status),
			StatusClass: rowStatusClass(row),
			Stale:       row.stale,
			Windows:     windows,
		})
	}

	data := frameData{
		StatusText:  statusText,
		StatusClass: statusClass,
		UpdatedText: updatedText,
		RowCount:    len(rows),
		Rows:        rows,
		Chart:       buildChartData(snapshot.Usage, location),
		Footer:      "Updates on change",
		FooterClass: "muted",
	}
	if len(snapshot.Errors) > 0 {
		data.Footer = compact(snapshot.Errors[0], 52)
		data.FooterClass = "error"
	}
	if snapshot.Usage != nil {
		data.Usage = &usageData{
			Tokens: formatTokens(snapshot.Usage.TodayTokens) + " tok",
			Cost:   fmt.Sprintf("%s%.2f", snapshot.Usage.Currency, snapshot.Usage.TodayCost),
		}
	}
	return data
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
	}
	if window.remaining != nil {
		remaining := clamp(*window.remaining)
		data.PercentText = fmt.Sprintf("%.0f%%", remaining)
		data.BarWidth = int(remaining)
		switch {
		case remaining <= 20:
			data.PercentClass = "urgent"
		case remaining <= 45:
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

func buildChartData(usage *quota.Usage, location *time.Location) *chartData {
	if usage == nil || len(usage.Days) == 0 {
		return nil
	}
	currency := usage.Currency
	if currency == "" {
		currency = "$"
	}
	var maxTokens int64
	var maxCost float64
	days := make([]chartDay, 0, len(usage.Days))
	for _, day := range usage.Days {
		if day.Tokens > maxTokens {
			maxTokens = day.Tokens
		}
		if day.Cost > maxCost {
			maxCost = day.Cost
		}
		days = append(days, chartDay{
			Label:      formatChartLabel(day.Date, location),
			TokenTitle: formatTokens(day.Tokens) + " tok",
			CostTitle:  fmt.Sprintf("%s%.2f", currency, day.Cost),
		})
	}
	for index, day := range usage.Days {
		days[index].TokenHeight = barHeight(float64(day.Tokens), float64(maxTokens))
		days[index].CostHeight = barHeight(day.Cost, maxCost)
	}
	return &chartData{
		Title: fmt.Sprintf("7d  %s tok  %s%.2f", formatTokens(sumTokens(usage.Days)), currency, sumCost(usage.Days)),
		Days:  days,
	}
}

func formatChartLabel(value string, location *time.Location) string {
	if parsed, err := time.ParseInLocation("2006-01-02", value, location); err == nil {
		return parsed.Format("01-02")
	}
	if len(value) >= 5 {
		return value[len(value)-5:]
	}
	return value
}

func barHeight(value, maximum float64) int {
	if value <= 0 || maximum <= 0 {
		return 0
	}
	height := int(value / maximum * 100)
	if height < 2 {
		return 2
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
