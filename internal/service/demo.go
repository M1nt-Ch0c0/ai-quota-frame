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
	return &quota.Usage{
		TodayTokens: 12345678,
		TodayCost:   4.21,
		Currency:    "$",
		Source:      activeQuotaSourceDemo,
	}, nil
}
