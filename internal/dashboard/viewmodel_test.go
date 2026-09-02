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
	if data.Usage.Today.Tokens != "999" || data.Usage.Today.Cost != "€9.99" {
		t.Fatalf("Usage.Today = %#v, want current usage values", data.Usage.Today)
	}
	if data.Usage.SevenDay.Tokens != "4.6M" || data.Usage.SevenDay.Cost != "€3.75" {
		t.Fatalf("Usage.SevenDay = %#v, want seven-day summary", data.Usage.SevenDay)
	}
	if data.Usage.Peak.Tokens != "3.4M" || data.Usage.Peak.Cost != "€2.50" || data.Usage.Peak.Note != "09-01" {
		t.Fatalf("Usage.Peak = %#v, want peak day summary", data.Usage.Peak)
	}
	if len(data.Usage.Days) != 2 || data.Usage.Days[0].Height != 35 || data.Usage.Days[1].Height != 100 {
		t.Fatalf("Usage.Days = %#v, want normalized compact chart", data.Usage.Days)
	}
	if data.Usage.Days[0].Value != "1.2M" || data.Usage.Days[1].Value != "3.4M" {
		t.Fatalf("Usage day values = %#v, want one-decimal compact labels", data.Usage.Days)
	}
}

func TestFormatTokensUsesOneDecimalScaleSuffix(t *testing.T) {
	for _, test := range []struct {
		value int64
		want  string
	}{
		{value: 1_400, want: "1.4k"},
		{value: 2_500_000, want: "2.5M"},
		{value: 1_000_000_000, want: "1.0B"},
	} {
		if got := formatTokens(test.value); got != test.want {
			t.Errorf("formatTokens(%d) = %q, want %q", test.value, got, test.want)
		}
	}
}

func TestBuildUsageDataKeepsOnlyLatestSevenDays(t *testing.T) {
	days := make([]quota.UsageDay, 8)
	for index := range days {
		days[index] = quota.UsageDay{Date: time.Date(2026, time.January, index+1, 0, 0, 0, 0, time.UTC).Format("2006-01-02"), Tokens: int64(index + 1)}
	}
	data := buildUsageData(&quota.Usage{Currency: "$", Days: days})
	if len(data.Days) != 7 || data.Days[0].Label != "01-02" || data.Days[6].Label != "01-08" {
		t.Fatalf("Days = %#v, want latest seven dates", data.Days)
	}
	if data.SevenDay.Tokens != "35" || data.Peak.Note != "01-08" {
		t.Fatalf("summary = %#v/%#v, want values based on latest seven days", data.SevenDay, data.Peak)
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
