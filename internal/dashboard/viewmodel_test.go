package dashboard

import (
	"testing"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/quota"
)

func TestBuildFrameDataSummarizesSevenDayUsage(t *testing.T) {
	data := buildFrameData(quota.Snapshot{
		Usage: &quota.Usage{
			TodayTokens: 999,
			TodayCost:   9.99,
			Currency:    "€",
			Days: []quota.UsageDay{
				{Date: "2026-08-31", Tokens: 1_200_000, Cost: 1.25},
				{Date: "2026-09-01", Tokens: 3_400_000, Cost: 2.50},
			},
		},
	}, time.UTC, nil)

	if data.Usage == nil {
		t.Fatal("Usage = nil, want seven-day summary")
	}
	if data.Usage.Tokens != "4.6M tok" {
		t.Fatalf("Usage.Tokens = %q, want %q", data.Usage.Tokens, "4.6M tok")
	}
	if data.Usage.Cost != "€3.75" {
		t.Fatalf("Usage.Cost = %q, want %q", data.Usage.Cost, "€3.75")
	}
}

func TestBuildFrameDataDoesNotUseTodayFieldsWithoutDays(t *testing.T) {
	data := buildFrameData(quota.Snapshot{
		Usage: &quota.Usage{TodayTokens: 123_000, TodayCost: 4.56, Currency: "$"},
	}, time.UTC, nil)

	if data.Usage != nil {
		t.Fatalf("Usage = %#v, want nil/unavailable when Days is empty", data.Usage)
	}
}

func TestBuildBarSegmentsUseRetroThresholdColorsAndPartialCell(t *testing.T) {
	remaining := 78.0
	segments := buildBarSegments(&remaining)
	if len(segments) != 10 {
		t.Fatalf("segment count = %d, want 10", len(segments))
	}
	wantClasses := []string{"urgent", "warn", "warn", "warn", "ok", "ok", "ok", "ok", "ok", "ok"}
	wantWidths := []int{100, 100, 100, 100, 100, 100, 100, 80, 0, 0}
	for index := range segments {
		if segments[index].FillClass != wantClasses[index] || segments[index].FillWidth != wantWidths[index] {
			t.Fatalf("segment %d = %#v, want class=%q width=%d", index, segments[index], wantClasses[index], wantWidths[index])
		}
	}

	five := 5.0
	segments = buildBarSegments(&five)
	if segments[0].FillClass != "urgent" || segments[0].FillWidth != 50 {
		t.Fatalf("5%% first segment = %#v, want half-filled urgent cell", segments[0])
	}
	for index := 1; index < len(segments); index++ {
		if segments[index].FillWidth != 0 {
			t.Fatalf("5%% segment %d width = %d, want 0", index, segments[index].FillWidth)
		}
	}
}

func TestBuildWindowDataUsesRequestedTenAndFortyPercentThresholds(t *testing.T) {
	for _, test := range []struct {
		remaining float64
		want      string
	}{
		{remaining: 9, want: "urgent"},
		{remaining: 10, want: "warn"},
		{remaining: 39, want: "warn"},
		{remaining: 40, want: "ok"},
	} {
		data := buildWindowData(displayWindow{remaining: &test.remaining}, time.UTC)
		if data.PercentClass != test.want {
			t.Errorf("remaining %.0f class = %q, want %q", test.remaining, data.PercentClass, test.want)
		}
	}
}
