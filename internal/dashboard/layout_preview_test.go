package dashboard

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/quota"
)

func TestWriteLayoutPreviews(t *testing.T) {
	dir := os.Getenv("LAYOUT_PREVIEW_DIR")
	if dir == "" {
		t.Skip("set LAYOUT_PREVIEW_DIR to write row-density previews")
	}
	if !chromeAvailable() {
		t.Fatal("chromium or google-chrome is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	snapshot := layoutPreviewSnapshot()
	lists := map[int]string{
		3: "codex,claude:CLAUDE,xai:GROK",
		4: "codex,claude:CLAUDE,xai:GROK,kimi:KIMI",
		5: "codex,claude:CLAUDE,xai:GROK,kimi:KIMI,antigravity:AGY",
	}
	for _, theme := range []string{"mint-skin", "classic", "arcade", "pop"} {
		for _, n := range []int{3, 4, 5} {
			renderer, err := New(time.FixedZone("UTC+8", 8*60*60))
			if err != nil {
				t.Fatal(err)
			}
			if err := renderer.SetTheme(theme); err != nil {
				t.Fatal(err)
			}
			providers, err := ParseDisplayProviders(lists[n])
			if err != nil {
				t.Fatal(err)
			}
			renderer.SetDisplayProviders(providers)
			html, err := renderer.renderHTML(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			want := "density-" + rowDensity(theme, themeRowHeight(theme, n))
			if !strings.Contains(html, want) {
				t.Fatalf("%s x%d html missing %s", theme, n, want)
			}
			payload, err := renderer.Render(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, theme+"-"+strconv.Itoa(n)+".png")
			if err := os.WriteFile(path, payload, 0o644); err != nil {
				t.Fatal(err)
			}
			t.Log(path)
		}
	}
}

func layoutPreviewSnapshot() quota.Snapshot {
	updated := time.Date(2026, 10, 20, 9, 30, 0, 0, time.UTC)
	reset := updated.Add(3 * time.Hour)
	week := updated.Add(5 * 24 * time.Hour)
	cardA := updated.AddDate(0, 0, 3)
	cardB := updated.AddDate(0, 0, 12)
	cardC := updated.AddDate(0, 0, 21)
	window := func(id, label string, remaining float64, at *time.Time) quota.Window {
		return quota.Window{ID: id, Label: label, RemainingPercent: quota.Percent(remaining), ResetsAt: at, Source: "demo"}
	}
	cards := []quota.ResetCard{{ExpiresAt: &cardA}, {ExpiresAt: &cardB}, {ExpiresAt: &cardC}}
	return quota.Snapshot{
		SchemaVersion: quota.SchemaVersion,
		DataUpdatedAt: updated,
		Accounts: []quota.Account{
			{Provider: "codex", Plan: "plus", Status: "ok", Windows: []quota.Window{window("7d", "7d", 62, &week)}, ResetCards: cards},
			{Provider: "claude", Plan: "max", Status: "low", Windows: []quota.Window{window("5h", "5h", 18, &reset), window("7d", "7d", 41, &week)}},
			{Provider: "xai", Plan: "supergrok", Status: "ok", Windows: []quota.Window{window("7d", "7d", 86, &week)}},
			{Provider: "kimi", Plan: "advanced", Status: "ok", Windows: []quota.Window{window("5h", "5h", 74, &reset), window("7d", "7d", 55, &week)}},
			{Provider: "antigravity", Plan: "pro", Status: "exhausted", Windows: []quota.Window{window("5h", "5h", 4, &reset), window("7d", "7d", 9, &week)}},
		},
		Usage: testSnapshot().Usage,
	}
}
