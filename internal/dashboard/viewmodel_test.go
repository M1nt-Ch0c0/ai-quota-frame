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
