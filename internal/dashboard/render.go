package dashboard

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"strings"
	"sync"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/quota"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

const (
	Width  = 800
	Height = 480
)

var (
	background = color.RGBA{R: 246, G: 244, B: 235, A: 255}
	paper      = color.RGBA{R: 255, G: 255, B: 255, A: 255}
	ink        = color.RGBA{R: 20, G: 20, B: 18, A: 255}
	muted      = color.RGBA{R: 92, G: 91, B: 84, A: 255}
	blue       = color.RGBA{R: 17, G: 67, B: 151, A: 255}
	green      = color.RGBA{R: 35, G: 104, B: 61, A: 255}
	yellow     = color.RGBA{R: 211, G: 181, B: 0, A: 255}
	red        = color.RGBA{R: 143, G: 25, B: 14, A: 255}
)

type Renderer struct {
	mu       sync.Mutex
	location *time.Location
	title    font.Face
	heading  font.Face
	large    font.Face
	body     font.Face
	small    font.Face
}

type displayWindow struct {
	label     string
	remaining *float64
	reset     *time.Time
}

type displayRow struct {
	provider string
	detail   string
	status   string
	stale    bool
	windows  []displayWindow
}

func New(location *time.Location) (*Renderer, error) {
	if location == nil {
		location = time.Local
	}
	regular, err := opentype.Parse(goregular.TTF)
	if err != nil {
		return nil, err
	}
	bold, err := opentype.Parse(gobold.TTF)
	if err != nil {
		return nil, err
	}
	face := func(parsed *opentype.Font, size float64) (font.Face, error) {
		return opentype.NewFace(parsed, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	}
	title, err := face(bold, 27)
	if err != nil {
		return nil, err
	}
	heading, err := face(bold, 20)
	if err != nil {
		return nil, err
	}
	large, err := face(bold, 24)
	if err != nil {
		return nil, err
	}
	body, err := face(regular, 15)
	if err != nil {
		return nil, err
	}
	small, err := face(regular, 12.5)
	if err != nil {
		return nil, err
	}
	return &Renderer{location: location, title: title, heading: heading, large: large, body: body, small: small}, nil
}

func (renderer *Renderer) Render(snapshot quota.Snapshot) ([]byte, error) {
	// The font faces keep internal glyph state and are not safe for concurrent
	// use by multiple HTTP requests.
	renderer.mu.Lock()
	defer renderer.mu.Unlock()
	canvas := renderer.draw(snapshot)
	var output bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := encoder.Encode(&output, canvas); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func (renderer *Renderer) draw(snapshot quota.Snapshot) *image.RGBA {
	canvas := image.NewRGBA(image.Rect(0, 0, Width, Height))
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{C: background}, image.Point{}, draw.Src)
	draw.Draw(canvas, image.Rect(0, 0, 8, Height), &image.Uniform{C: blue}, image.Point{}, draw.Src)

	renderer.text(canvas, 26, 42, "AI QUOTA", renderer.title, ink)
	titleWidth := renderer.measure("AI QUOTA", renderer.title)

	statusText := "LIVE"
	statusColor := green
	if snapshot.Stale {
		statusText = "STALE"
		statusColor = red
	} else if snapshotUsesSource(snapshot, "demo") {
		statusText = "DEMO"
		statusColor = blue
	}
	renderer.badge(canvas, 26+titleWidth+12, 22, statusText, statusColor)

	updated := "Waiting for first refresh"
	if !snapshot.DataUpdatedAt.IsZero() {
		updated = "Data " + snapshot.DataUpdatedAt.In(renderer.location).Format("01-02 15:04")
	}
	renderer.textRight(canvas, 776, 40, updated, renderer.body, muted)

	rows := aggregateRows(snapshot.Accounts)
	for index, row := range rows {
		renderer.row(canvas, 24, 66+index*118, 752, 106, row)
	}

	renderer.statsBar(canvas, 24, 424, 752, 42, snapshot)
	return canvas
}

func (renderer *Renderer) statsBar(canvas *image.RGBA, x, y, width, height int, snapshot quota.Snapshot) {
	draw.Draw(canvas, image.Rect(x, y, x+width, y+height), &image.Uniform{C: paper}, image.Point{}, draw.Src)
	drawBorder(canvas, image.Rect(x, y, x+width, y+height), ink, 2)

	baseline := y + 28
	if snapshot.Usage == nil {
		renderer.text(canvas, x+16, baseline, "Usage data unavailable", renderer.small, muted)
	} else {
		renderer.text(canvas, x+16, baseline, "Today", renderer.small, muted)
		tokens := formatTokens(snapshot.Usage.TodayTokens) + " tok"
		renderer.text(canvas, x+72, baseline, tokens, renderer.heading, ink)
		cost := fmt.Sprintf("%s%.2f", snapshot.Usage.Currency, snapshot.Usage.TodayCost)
		renderer.text(canvas, x+72+renderer.measure(tokens, renderer.heading)+14, baseline, cost, renderer.heading, green)
	}

	footer := "Updates on change"
	footerColor := muted
	if len(snapshot.Errors) > 0 {
		footer = compact(snapshot.Errors[0], 52)
		footerColor = red
	}
	renderer.textRight(canvas, x+width-16, baseline, footer, renderer.small, footerColor)
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

func (renderer *Renderer) row(canvas *image.RGBA, x, y, width, height int, row displayRow) {
	draw.Draw(canvas, image.Rect(x, y, x+width, y+height), &image.Uniform{C: paper}, image.Point{}, draw.Src)
	drawBorder(canvas, image.Rect(x, y, x+width, y+height), ink, 2)

	providerColor := blue
	if row.status == "low" || row.status == "exhausted" || row.status == "error" {
		providerColor = red
	} else if row.status == "disabled" || row.status == "missing" || row.status == "unknown" || row.stale {
		providerColor = yellow
	}
	draw.Draw(canvas, image.Rect(x, y, x+6, y+height), &image.Uniform{C: providerColor}, image.Point{}, draw.Src)
	renderer.text(canvas, x+16, y+32, row.provider, renderer.heading, ink)
	renderer.text(canvas, x+16, y+56, compact(row.detail, 26), renderer.small, muted)
	statusLabel := strings.ToUpper(row.status)
	if row.stale {
		statusLabel += " / STALE"
	}
	renderer.text(canvas, x+16, y+78, statusLabel, renderer.small, providerColor)

	if len(row.windows) == 0 {
		renderer.text(canvas, x+180, y+58, "No quota window available", renderer.body, muted)
		return
	}
	for index := 0; index < 2; index++ {
		startX := x + 180 + index*286
		if index >= len(row.windows) {
			continue
		}
		renderer.window(canvas, startX, y+14, 262, row.windows[index])
	}
}

func (renderer *Renderer) window(canvas *image.RGBA, x, y, width int, window displayWindow) {
	renderer.text(canvas, x, y+16, compact(window.label, 18), renderer.body, ink)
	percentText := "--%"
	remaining := 0.0
	barColor := muted
	if window.remaining != nil {
		remaining = clamp(*window.remaining)
		percentText = fmt.Sprintf("%.0f%%", remaining)
		switch {
		case remaining <= 20:
			barColor = red
		case remaining <= 45:
			barColor = yellow
		default:
			barColor = green
		}
	}
	renderer.textRight(canvas, x+width, y+18, percentText, renderer.large, barColor)

	bar := image.Rect(x, y+28, x+width, y+48)
	draw.Draw(canvas, bar, &image.Uniform{C: background}, image.Point{}, draw.Src)
	drawBorder(canvas, bar, ink, 2)
	if window.remaining != nil && remaining > 0 {
		fillWidth := int(float64(width-4) * remaining / 100)
		if fillWidth < 1 {
			fillWidth = 1
		}
		draw.Draw(canvas, image.Rect(x+2, y+30, x+2+fillWidth, y+46), &image.Uniform{C: barColor}, image.Point{}, draw.Src)
	}

	resetText := "Reset --"
	if window.reset != nil {
		resetText = "Reset " + window.reset.In(renderer.location).Format("01-02 15:04")
	}
	renderer.text(canvas, x, y+70, resetText, renderer.small, muted)
}

func (renderer *Renderer) badge(canvas *image.RGBA, x, y int, label string, fill color.Color) {
	width := renderer.measure(label, renderer.small) + 20
	draw.Draw(canvas, image.Rect(x, y, x+width, y+26), &image.Uniform{C: fill}, image.Point{}, draw.Src)
	drawer := &font.Drawer{Dst: canvas, Src: image.NewUniform(paper), Face: renderer.small}
	textWidth := drawer.MeasureString(label).Ceil()
	drawer.Dot = fixedPoint(x+(width-textWidth)/2, y+18)
	drawer.DrawString(label)
}

func (renderer *Renderer) measure(value string, face font.Face) int {
	drawer := &font.Drawer{Face: face}
	return drawer.MeasureString(value).Ceil()
}

func (renderer *Renderer) text(canvas *image.RGBA, x, baseline int, value string, face font.Face, textColor color.Color) {
	drawer := &font.Drawer{Dst: canvas, Src: image.NewUniform(textColor), Face: face, Dot: fixedPoint(x, baseline)}
	drawer.DrawString(value)
}

func (renderer *Renderer) textRight(canvas *image.RGBA, right, baseline int, value string, face font.Face, textColor color.Color) {
	drawer := &font.Drawer{Dst: canvas, Src: image.NewUniform(textColor), Face: face}
	width := drawer.MeasureString(value).Ceil()
	drawer.Dot = fixedPoint(right-width, baseline)
	drawer.DrawString(value)
}

func fixedPoint(x, y int) fixed.Point26_6 {
	return fixed.P(x, y)
}

func drawBorder(canvas *image.RGBA, rectangle image.Rectangle, border color.Color, thickness int) {
	draw.Draw(canvas, image.Rect(rectangle.Min.X, rectangle.Min.Y, rectangle.Max.X, rectangle.Min.Y+thickness), &image.Uniform{C: border}, image.Point{}, draw.Src)
	draw.Draw(canvas, image.Rect(rectangle.Min.X, rectangle.Max.Y-thickness, rectangle.Max.X, rectangle.Max.Y), &image.Uniform{C: border}, image.Point{}, draw.Src)
	draw.Draw(canvas, image.Rect(rectangle.Min.X, rectangle.Min.Y, rectangle.Min.X+thickness, rectangle.Max.Y), &image.Uniform{C: border}, image.Point{}, draw.Src)
	draw.Draw(canvas, image.Rect(rectangle.Max.X-thickness, rectangle.Min.Y, rectangle.Max.X, rectangle.Max.Y), &image.Uniform{C: border}, image.Point{}, draw.Src)
}

func aggregateRows(accounts []quota.Account) []displayRow {
	providers := []struct {
		ids   []string
		group string
		label string
	}{
		{[]string{"codex"}, "codex", "CODEX"},
		{[]string{"xai"}, "xai", "GROK"},
		{[]string{"kimi"}, "kimi", "KIMI"},
	}
	rows := make([]displayRow, 0, len(providers))
	for _, provider := range providers {
		var matching []quota.Account
		for _, account := range accounts {
			if containsString(provider.ids, account.Provider) {
				matching = append(matching, account)
			}
		}
		if len(matching) == 0 {
			rows = append(rows, displayRow{provider: provider.label, detail: "No OAuth account", status: "missing"})
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
			provider: provider.label,
			detail:   detail,
			status:   status,
			stale:    hasStale,
			windows:  aggregateWindows(provider.group, effective),
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
	case "codex", "kimi":
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
