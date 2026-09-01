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
			Logo:        ProviderLogoSVG(row.group),
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
		Usage:       buildUsageData(snapshot.Usage),
		Footer:      "Updates on change",
		FooterClass: "muted",
	}
	if len(snapshot.Errors) > 0 {
		data.Footer = compact(snapshot.Errors[0], 52)
		data.FooterClass = "error"
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

func buildUsageData(usage *quota.Usage) *usageData {
	if usage == nil || len(usage.Days) == 0 {
		return nil
	}
	currency := usage.Currency
	if currency == "" {
		currency = "$"
	}
	return &usageData{
		Tokens: formatTokens(sumTokens(usage.Days)) + " tok",
		Cost:   fmt.Sprintf("%s%.2f", currency, sumCost(usage.Days)),
	}
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
