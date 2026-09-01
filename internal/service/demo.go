package service

import (
	"context"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/quota"
)

type DemoFetcher struct {
	Now func() time.Time
}

func (fetcher DemoFetcher) Fetch(context.Context) ([]quota.Account, []string, error) {
	now := time.Now
	if fetcher.Now != nil {
		now = fetcher.Now
	}
	observedAt := now().UTC()
	resetFiveHours := observedAt.Add(2*time.Hour + 14*time.Minute)
	resetWeek := observedAt.Add(3*24*time.Hour + 7*time.Hour)
	return []quota.Account{
		{
			Provider: "codex", Name: "j***@example.com", Plan: "plus", Status: "ok", LastFreshAt: &observedAt,
			Windows: []quota.Window{
				{ID: "5h", Label: "5h", UsedPercent: quota.Percent(38), RemainingPercent: quota.Percent(62), ResetsAt: &resetFiveHours, ObservedAt: &observedAt, Source: activeQuotaSourceDemo},
				{ID: "7d", Label: "7d", UsedPercent: quota.Percent(57), RemainingPercent: quota.Percent(43), ResetsAt: &resetWeek, ObservedAt: &observedAt, Source: activeQuotaSourceDemo},
			},
		},
		{
			Provider: "xai", Name: "w***@example.com", Status: "unknown", Stale: true,
			Warning: "OAuth is connected, but no verified quota adapter is available for this provider",
			Windows: []quota.Window{},
		},
		{
			Provider: "kimi", Name: "kimi", Plan: "advanced", Status: "ok", LastFreshAt: &observedAt,
			Windows: []quota.Window{
				{ID: "5h", Label: "5h", UsedPercent: quota.Percent(15), RemainingPercent: quota.Percent(85), ResetsAt: &resetFiveHours, ObservedAt: &observedAt, Source: activeQuotaSourceDemo},
				{ID: "7d", Label: "7d", UsedPercent: quota.Percent(4), RemainingPercent: quota.Percent(96), ResetsAt: &resetWeek, ObservedAt: &observedAt, Source: activeQuotaSourceDemo},
			},
		},
	}, nil, nil
}

const activeQuotaSourceDemo = "demo"

// DemoUsageFetcher returns a fixed usage fixture so DEMO_MODE can exercise the
// bottom stats bar without a running usage collector.
type DemoUsageFetcher struct{}

func (fetcher DemoUsageFetcher) FetchUsage(context.Context) (*quota.Usage, error) {
	days := make([]quota.UsageDay, 7)
	tokens := []int64{4_200_000, 6_800_000, 5_100_000, 9_400_000, 7_200_000, 3_600_000, 12_345_678}
	costs := []float64{1.12, 2.05, 1.48, 3.10, 2.40, 0.96, 4.21}
	base := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -6)
	for index := range days {
		days[index] = quota.UsageDay{
			Date:   base.AddDate(0, 0, index).Format("2006-01-02"),
			Tokens: tokens[index],
			Cost:   costs[index],
		}
	}
	return &quota.Usage{
		TodayTokens: 12345678,
		TodayCost:   4.21,
		Currency:    "$",
		Source:      activeQuotaSourceDemo,
		Days:        days,
	}, nil
}
