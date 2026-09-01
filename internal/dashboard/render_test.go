package dashboard

import (
	"bytes"
	"image/png"
	"sync"
	"testing"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/quota"
)

func TestRenderProducesExpectedPNGDimensions(t *testing.T) {
	renderer, err := New(time.UTC)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	payload, err := renderer.Render(testSnapshot())
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	image, err := png.Decode(bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("png.Decode() error = %v", err)
	}
	if got := image.Bounds().Dx(); got != Width {
		t.Fatalf("PNG width = %d, want %d", got, Width)
	}
	if got := image.Bounds().Dy(); got != Height {
		t.Fatalf("PNG height = %d, want %d", got, Height)
	}
}

func TestRenderIgnoresRefreshScheduleTimestamps(t *testing.T) {
	renderer, err := New(time.UTC)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	first := testSnapshot()
	second := first
	second.RefreshedAt = first.RefreshedAt.Add(17 * time.Minute)
	second.NextRefreshAt = first.NextRefreshAt.Add(23 * time.Minute)

	firstPNG, err := renderer.Render(first)
	if err != nil {
		t.Fatalf("Render(first) error = %v", err)
	}
	secondPNG, err := renderer.Render(second)
	if err != nil {
		t.Fatalf("Render(second) error = %v", err)
	}
	if !bytes.Equal(firstPNG, secondPNG) {
		t.Fatal("PNG bytes changed when only RefreshedAt and NextRefreshAt changed")
	}
}

func TestSnapshotUsesSourceDetectsDemoData(t *testing.T) {
	snapshot := testSnapshot()
	if snapshotUsesSource(snapshot, "demo") {
		t.Fatal("production fixture was classified as demo data")
	}
	snapshot.Accounts[0].Windows[0].Source = "demo"
	if !snapshotUsesSource(snapshot, "demo") {
		t.Fatal("demo window source was not detected")
	}
}

func TestRenderIsSafeForConcurrentHTTPRequests(t *testing.T) {
	renderer, err := New(time.UTC)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	const workers = 24
	errors := make(chan error, workers)
	var group sync.WaitGroup
	for index := 0; index < workers; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			payload, renderErr := renderer.Render(testSnapshot())
			if renderErr != nil {
				errors <- renderErr
				return
			}
			if _, decodeErr := png.Decode(bytes.NewReader(payload)); decodeErr != nil {
				errors <- decodeErr
			}
		}()
	}
	group.Wait()
	close(errors)
	for err := range errors {
		t.Errorf("concurrent Render() error = %v", err)
	}
}

func TestAggregateRowsMarksMixedFreshAndFallbackDataStale(t *testing.T) {
	rows := aggregateRows([]quota.Account{
		{
			Provider: "codex", Name: "fresh", Status: "ok",
			Windows: []quota.Window{{ID: "5h", Label: "5h", RemainingPercent: quota.Percent(80), Source: "oauth_internal_endpoint"}},
		},
		{
			Provider: "codex", Name: "fallback", Status: "ok", Stale: true,
			Windows: []quota.Window{{ID: "7d", Label: "7d", RemainingPercent: quota.Percent(60), Source: "cliproxy_headers"}},
		},
	})
	if len(rows) == 0 || rows[0].provider != "CODEX" {
		t.Fatalf("rows = %#v", rows)
	}
	if rows[0].status != "ok" || !rows[0].stale {
		t.Fatalf("Codex row status/stale = %q/%v, want ok/true", rows[0].status, rows[0].stale)
	}
}

func TestAggregateRowsKeepsQuotaUrgencyOrthogonalToStaleness(t *testing.T) {
	rows := aggregateRows([]quota.Account{
		{
			Provider: "codex", Name: "fresh exhausted", Status: "exhausted",
			Windows: []quota.Window{{ID: "5h", Label: "5h", RemainingPercent: quota.Percent(0), Source: "oauth_internal_endpoint"}},
		},
		{
			Provider: "codex", Name: "fallback", Status: "ok", Stale: true,
			Windows: []quota.Window{{ID: "7d", Label: "7d", RemainingPercent: quota.Percent(60), Source: "cliproxy_headers"}},
		},
	})
	if rows[0].status != "exhausted" || !rows[0].stale {
		t.Fatalf("Codex row status/stale = %q/%v, want exhausted/true", rows[0].status, rows[0].stale)
	}
}

func TestAggregateRowsIgnoresDisabledAccountWhenAnEnabledAccountExists(t *testing.T) {
	rows := aggregateRows([]quota.Account{
		{Provider: "codex", Name: "disabled", Status: "disabled", Windows: []quota.Window{}},
		{
			Provider: "codex", Name: "healthy", Status: "ok",
			Windows: []quota.Window{{ID: "5h", Label: "5h", RemainingPercent: quota.Percent(80)}},
		},
	})
	if rows[0].status != "ok" || rows[0].detail != "1 active / 2 total" {
		t.Fatalf("Codex row status/detail = %q/%q, want ok/1 active / 2 total", rows[0].status, rows[0].detail)
	}
	if len(rows[0].windows) != 1 || rows[0].windows[0].remaining == nil {
		t.Fatalf("Codex row lost the enabled account window: %#v", rows[0])
	}
}

func TestAggregateWindowsPrefersFiveHourAndWeeklyForKimi(t *testing.T) {
	windows := aggregateWindows("kimi", []quota.Account{
		{Provider: "kimi", Windows: []quota.Window{
			{ID: "7d", Label: "7d", RemainingPercent: quota.Percent(96)},
			{ID: "5h", Label: "5h", RemainingPercent: quota.Percent(85)},
		}},
	})
	if len(windows) != 2 || windows[0].label != "5h" || windows[1].label != "7d" {
		t.Fatalf("Kimi windows = %#v, want 5h then 7d", windows)
	}
}

func TestAggregateRowsShowsGrokWithoutQuotaWindows(t *testing.T) {
	rows := aggregateRows([]quota.Account{
		{Provider: "xai", Name: "w***@example.com", Status: "unknown", Stale: true, Warning: "no verified quota adapter", Windows: []quota.Window{}},
	})
	if len(rows) != 3 || rows[1].provider != "GROK" {
		t.Fatalf("rows = %#v", rows)
	}
	if rows[1].status != "unknown" || !rows[1].stale || len(rows[1].windows) != 0 {
		t.Fatalf("GROK row = %#v, want unknown/stale without windows", rows[1])
	}
}

func testSnapshot() quota.Snapshot {
	dataUpdatedAt := time.Date(2026, time.August, 30, 1, 30, 0, 0, time.UTC)
	resetAt := dataUpdatedAt.Add(3 * time.Hour)
	return quota.Snapshot{
		SchemaVersion: quota.SchemaVersion,
		RefreshedAt:   dataUpdatedAt.Add(2 * time.Minute),
		DataUpdatedAt: dataUpdatedAt,
		NextRefreshAt: dataUpdatedAt.Add(7 * time.Minute),
		Accounts: []quota.Account{
			{
				Provider: "codex",
				Name:     "c***@example.com",
				Plan:     "plus",
				Status:   "ok",
				Windows: []quota.Window{
					{
						ID:               "5h",
						Label:            "5h",
						UsedPercent:      quota.Percent(38),
						RemainingPercent: quota.Percent(62),
						ResetsAt:         &resetAt,
						Source:           "test",
					},
				},
			},
		},
	}
}
