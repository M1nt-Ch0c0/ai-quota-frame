package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/quota"
)

type fetchResult struct {
	accounts []quota.Account
	errors   []string
	err      error
}

type sequenceFetcher struct {
	results []fetchResult
	index   int
}

func (fetcher *sequenceFetcher) Fetch(context.Context) ([]quota.Account, []string, error) {
	result := fetcher.results[fetcher.index]
	if fetcher.index < len(fetcher.results)-1 {
		fetcher.index++
	}
	return result.accounts, result.errors, result.err
}

func TestRefreshMarksSnapshotStaleWithoutFreshEnabledQuota(t *testing.T) {
	tests := []struct {
		name     string
		accounts []quota.Account
	}{
		{name: "no accounts", accounts: []quota.Account{}},
		{name: "disabled only", accounts: []quota.Account{{Provider: "codex", Name: "disabled", Status: "disabled", Windows: []quota.Window{}}}},
		{name: "passive fallback only", accounts: []quota.Account{{
			Provider: "codex", Name: "fallback", Status: "ok", Stale: true,
			Windows: []quota.Window{{ID: "5h", Label: "5h", RemainingPercent: quota.Percent(50), Source: "cliproxy_headers"}},
			Warning: "active refresh failed",
		}}},
		{name: "reset only window", accounts: []quota.Account{{
			Provider: "codex", Name: "reset-only", Status: "unknown",
			Windows: []quota.Window{{ID: "5h", Label: "5h", Source: "oauth_internal_endpoint"}},
		}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := New(&sequenceFetcher{results: []fetchResult{{accounts: test.accounts}}}, 5*time.Minute)
			service.now = func() time.Time { return time.Date(2026, 8, 30, 2, 0, 0, 0, time.UTC) }
			if err := service.Refresh(context.Background()); err != nil {
				t.Fatalf("Refresh() error = %v", err)
			}
			snapshot, ok := service.Snapshot()
			if !ok {
				t.Fatal("Snapshot() was not ready")
			}
			if !snapshot.Stale {
				t.Fatal("snapshot.Stale = false, want true")
			}
		})
	}
}

func TestRefreshKeepsSnapshotFreshWhenAnyAccountHasFreshQuota(t *testing.T) {
	accounts := []quota.Account{
		{Provider: "codex", Name: "failed", Status: "error", Windows: []quota.Window{}, Error: "failed"},
		{Provider: "claude", Name: "fresh", Status: "ok", Windows: []quota.Window{{
			ID: "5h", Label: "5h", RemainingPercent: quota.Percent(70), Source: "oauth_internal_endpoint",
		}}},
	}
	service := New(&sequenceFetcher{results: []fetchResult{{accounts: accounts}}}, 5*time.Minute)
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	snapshot, _ := service.Snapshot()
	if snapshot.Stale {
		t.Fatal("snapshot.Stale = true, want false")
	}
}

func TestRefreshRetainsPreviousWindowsAfterAccountFailure(t *testing.T) {
	resetAt := time.Date(2026, 8, 30, 7, 0, 0, 0, time.UTC)
	fetcher := &sequenceFetcher{results: []fetchResult{
		{accounts: []quota.Account{{
			Provider: "codex", Name: "account", Status: "ok",
			Windows: []quota.Window{{ID: "5h", Label: "5h", RemainingPercent: quota.Percent(65), ResetsAt: &resetAt, Source: "oauth_internal_endpoint"}},
		}}},
		{accounts: []quota.Account{{Provider: "codex", Name: "account", Status: "error", Windows: []quota.Window{}, Error: "refresh failed"}}},
	}}
	service := New(fetcher, 5*time.Minute)
	now := time.Date(2026, 8, 30, 2, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatalf("first Refresh() error = %v", err)
	}
	first, _ := service.Snapshot()
	now = now.Add(5 * time.Minute)
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatalf("second Refresh() error = %v", err)
	}
	second, _ := service.Snapshot()
	if len(second.Accounts) != 1 || len(second.Accounts[0].Windows) != 1 {
		t.Fatalf("retained accounts = %#v", second.Accounts)
	}
	if got := *second.Accounts[0].Windows[0].RemainingPercent; got != 65 {
		t.Fatalf("retained remaining = %v, want 65", got)
	}
	if second.Accounts[0].Warning == "" || !second.Stale {
		t.Fatalf("warning = %q, stale = %v", second.Accounts[0].Warning, second.Stale)
	}
	if !second.DataUpdatedAt.After(first.DataUpdatedAt) {
		t.Fatalf("DataUpdatedAt did not advance after status changed: first=%v second=%v", first.DataUpdatedAt, second.DataUpdatedAt)
	}
}

func TestRefreshPreservesPreviousSnapshotOnFetcherFailure(t *testing.T) {
	upstreamErr := errors.New("management unavailable")
	fetcher := &sequenceFetcher{results: []fetchResult{
		{accounts: []quota.Account{{Provider: "codex", Name: "account", Status: "ok", Windows: []quota.Window{{ID: "5h", Label: "5h", RemainingPercent: quota.Percent(80), Source: "test"}}}}},
		{err: upstreamErr},
	}}
	service := New(fetcher, 5*time.Minute)
	now := time.Date(2026, 8, 30, 2, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatalf("first Refresh() error = %v", err)
	}
	now = now.Add(5 * time.Minute)
	if err := service.Refresh(context.Background()); !errors.Is(err, upstreamErr) {
		t.Fatalf("second Refresh() error = %v, want %v", err, upstreamErr)
	}
	snapshot, _ := service.Snapshot()
	if !snapshot.Stale || len(snapshot.Accounts) != 1 || len(snapshot.Accounts[0].Windows) != 1 {
		t.Fatalf("snapshot after failure = %#v", snapshot)
	}
}

func TestInitialAuthDiscoveryFailureDoesNotMakeServiceReady(t *testing.T) {
	service := New(&sequenceFetcher{results: []fetchResult{{err: errors.New("management unavailable")}}}, 5*time.Minute)
	if err := service.Refresh(context.Background()); err == nil {
		t.Fatal("Refresh() error = nil")
	}
	snapshot, ready := service.Snapshot()
	if ready {
		t.Fatal("Snapshot() ready = true before any completed auth discovery")
	}
	if !snapshot.Stale {
		t.Fatal("diagnostic snapshot stale = false, want true")
	}
}

func TestLastFreshAtDoesNotAdvanceWhenOnlyRetainedDataRemains(t *testing.T) {
	fetcher := &sequenceFetcher{results: []fetchResult{
		{accounts: []quota.Account{{Provider: "codex", Name: "account", Status: "ok", Windows: []quota.Window{{ID: "5h", RemainingPercent: quota.Percent(70)}}}}},
		{accounts: []quota.Account{{Provider: "codex", Name: "account", Status: "error", Windows: []quota.Window{}, Error: "failed"}}},
	}}
	service := New(fetcher, 5*time.Minute)
	now := time.Date(2026, 8, 30, 2, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatalf("first Refresh() error = %v", err)
	}
	first, _ := service.Snapshot()
	if first.LastFreshAt == nil || !first.LastFreshAt.Equal(now) {
		t.Fatalf("first LastFreshAt = %v, want %v", first.LastFreshAt, now)
	}
	now = now.Add(5 * time.Minute)
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatalf("second Refresh() error = %v", err)
	}
	second, _ := service.Snapshot()
	if second.LastFreshAt == nil || !second.LastFreshAt.Equal(*first.LastFreshAt) {
		t.Fatalf("second LastFreshAt = %v, want retained %v", second.LastFreshAt, first.LastFreshAt)
	}
}

func TestOlderPassiveFallbackDoesNotReplaceNewerActiveSnapshot(t *testing.T) {
	activeAt := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	passiveAt := activeAt.Add(-23 * time.Hour)
	fetcher := &sequenceFetcher{results: []fetchResult{
		{accounts: []quota.Account{{
			RetentionID: "account-id", Provider: "codex", Name: "account", Status: "ok", LastFreshAt: &activeAt,
			Windows: []quota.Window{{ID: "5h", RemainingPercent: quota.Percent(80), ObservedAt: &activeAt, Source: "oauth_internal_endpoint"}},
		}}},
		{accounts: []quota.Account{{
			RetentionID: "account-id", Provider: "codex", Name: "account", Status: "low", Stale: true,
			Windows: []quota.Window{{ID: "5h", RemainingPercent: quota.Percent(10), ObservedAt: &passiveAt, Source: "cliproxy_headers"}},
			Warning: "active refresh failed; showing a passive observation",
		}}},
	}}
	service := New(fetcher, 5*time.Minute)
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatalf("first Refresh() error = %v", err)
	}
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatalf("second Refresh() error = %v", err)
	}
	snapshot, _ := service.Snapshot()
	account := snapshot.Accounts[0]
	if got := *account.Windows[0].RemainingPercent; got != 80 {
		t.Fatalf("remaining = %v, want newer active value 80", got)
	}
	if !strings.Contains(account.Warning, "newer previous") || !account.Stale {
		t.Fatalf("account warning/stale = %q/%v", account.Warning, account.Stale)
	}
}

func TestObservationTimestampsDoNotAdvanceDisplayDataTime(t *testing.T) {
	firstObserved := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	secondObserved := firstObserved.Add(5 * time.Minute)
	firstReset := time.Date(2026, 8, 30, 11, 0, 5, 0, time.UTC)
	secondReset := time.Date(2026, 8, 30, 11, 0, 55, 0, time.UTC)
	fetcher := &sequenceFetcher{results: []fetchResult{
		{accounts: []quota.Account{{
			Provider: "codex", Name: "account", Status: "ok", LastFreshAt: &firstObserved,
			Windows: []quota.Window{{ID: "5h", UsedPercent: quota.Percent(20), RemainingPercent: quota.Percent(80), ResetsAt: &firstReset, ObservedAt: &firstObserved, Source: "oauth_internal_endpoint"}},
		}}},
		{accounts: []quota.Account{{
			Provider: "codex", Name: "account", Status: "ok", LastFreshAt: &secondObserved,
			Windows: []quota.Window{{ID: "5h", UsedPercent: quota.Percent(20), RemainingPercent: quota.Percent(80), ResetsAt: &secondReset, ObservedAt: &secondObserved, Source: "oauth_internal_endpoint"}},
		}}},
	}}
	service := New(fetcher, 5*time.Minute)
	now := firstObserved
	service.now = func() time.Time { return now }
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatalf("first Refresh() error = %v", err)
	}
	first, _ := service.Snapshot()
	now = secondObserved
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatalf("second Refresh() error = %v", err)
	}
	second, _ := service.Snapshot()
	if !second.DataUpdatedAt.Equal(first.DataUpdatedAt) {
		t.Fatalf("DataUpdatedAt advanced from %v to %v for observation timestamps only", first.DataUpdatedAt, second.DataUpdatedAt)
	}
	if second.LastFreshAt == nil || !second.LastFreshAt.Equal(secondObserved) {
		t.Fatalf("LastFreshAt = %v, want %v", second.LastFreshAt, secondObserved)
	}
}

func TestSnapshotReturnsIndependentAccountWindowAndErrorSlices(t *testing.T) {
	fetcher := &sequenceFetcher{results: []fetchResult{{
		accounts: []quota.Account{{
			Provider: "codex",
			Name:     "original account",
			Status:   "ok",
			Windows: []quota.Window{{
				ID:               "5h",
				Label:            "original window",
				RemainingPercent: quota.Percent(60),
				Source:           "oauth_internal_endpoint",
			}},
		}},
		errors: []string{"original fetch warning"},
	}}}
	service := New(fetcher, 5*time.Minute)
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	first, ok := service.Snapshot()
	if !ok {
		t.Fatal("Snapshot() was not ready")
	}
	first.Accounts[0].Name = "caller mutation"
	first.Accounts[0].Windows[0].Label = "caller mutation"
	*first.Accounts[0].Windows[0].RemainingPercent = 1
	first.Accounts[0].Windows = append(first.Accounts[0].Windows, quota.Window{ID: "extra"})
	first.Errors[0] = "caller mutation"

	second, _ := service.Snapshot()
	if second.Accounts[0].Name != "original account" {
		t.Fatalf("stored account name = %q after caller mutation", second.Accounts[0].Name)
	}
	if len(second.Accounts[0].Windows) != 1 || second.Accounts[0].Windows[0].Label != "original window" {
		t.Fatalf("stored windows changed after caller mutation: %#v", second.Accounts[0].Windows)
	}
	if got := *second.Accounts[0].Windows[0].RemainingPercent; got != 60 {
		t.Fatalf("stored remaining percent = %v after caller mutation, want 60", got)
	}
	if len(second.Errors) != 1 || second.Errors[0] != "original fetch warning" {
		t.Fatalf("stored errors changed after caller mutation: %#v", second.Errors)
	}
}

func TestRetainingWindowsDoesNotMutateFetcherOwnedFailure(t *testing.T) {
	failedAccounts := []quota.Account{{
		Provider: "codex",
		Name:     "account",
		Status:   "error",
		Windows:  []quota.Window{},
		Error:    "refresh failed",
	}}
	fetcher := &sequenceFetcher{results: []fetchResult{
		{accounts: []quota.Account{{
			Provider: "codex",
			Name:     "account",
			Status:   "ok",
			Windows: []quota.Window{{
				ID:               "5h",
				Label:            "5h",
				RemainingPercent: quota.Percent(65),
				Source:           "oauth_internal_endpoint",
			}},
		}}},
		{accounts: failedAccounts},
	}}
	service := New(fetcher, 5*time.Minute)
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatalf("first Refresh() error = %v", err)
	}
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatalf("second Refresh() error = %v", err)
	}

	if len(failedAccounts[0].Windows) != 0 {
		t.Fatalf("fetcher-owned windows were mutated: %#v", failedAccounts[0].Windows)
	}
	if failedAccounts[0].Warning != "" {
		t.Fatalf("fetcher-owned warning was mutated: %q", failedAccounts[0].Warning)
	}
	snapshot, _ := service.Snapshot()
	if len(snapshot.Accounts[0].Windows) != 1 || snapshot.Accounts[0].Warning == "" {
		t.Fatalf("service did not retain the old window independently: %#v", snapshot.Accounts[0])
	}
	if snapshot.Accounts[0].Status != "ok" || snapshot.Accounts[0].Error != "" || !snapshot.Accounts[0].Stale {
		t.Fatalf("retained account status/error/stale = %q/%q/%v, want ok/empty/true", snapshot.Accounts[0].Status, snapshot.Accounts[0].Error, snapshot.Accounts[0].Stale)
	}
}

func TestRetentionUsesHostOnlyIdentityWhenMaskedNamesCollide(t *testing.T) {
	fetcher := &sequenceFetcher{results: []fetchResult{
		{accounts: []quota.Account{
			{RetentionID: "alice-id", Provider: "codex", Name: "a***@example.com", Status: "ok", Windows: []quota.Window{{ID: "5h", RemainingPercent: quota.Percent(10)}}},
			{RetentionID: "adam-id", Provider: "codex", Name: "a***@example.com", Status: "ok", Windows: []quota.Window{{ID: "5h", RemainingPercent: quota.Percent(90)}}},
		}},
		{accounts: []quota.Account{
			{RetentionID: "adam-id", Provider: "codex", Name: "a***@example.com", Status: "error", Windows: []quota.Window{}, Error: "failed"},
			{RetentionID: "alice-id", Provider: "codex", Name: "a***@example.com", Status: "error", Windows: []quota.Window{}, Error: "failed"},
		}},
	}}
	service := New(fetcher, 5*time.Minute)
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatalf("first Refresh() error = %v", err)
	}
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatalf("second Refresh() error = %v", err)
	}
	snapshot, _ := service.Snapshot()
	want := map[string]float64{"alice-id": 10, "adam-id": 90}
	for _, account := range snapshot.Accounts {
		if len(account.Windows) != 1 || account.Windows[0].RemainingPercent == nil {
			t.Fatalf("retained account = %#v", account)
		}
		if got := *account.Windows[0].RemainingPercent; got != want[account.RetentionID] {
			t.Fatalf("identity %q retained %v, want %v", account.RetentionID, got, want[account.RetentionID])
		}
	}
}

type stubUsageFetcher struct {
	usage *quota.Usage
	err   error
}

func (fetcher *stubUsageFetcher) FetchUsage(context.Context) (*quota.Usage, error) {
	return fetcher.usage, fetcher.err
}

func TestRefreshAttachesUsageAndRetainsItOnCollectorFailure(t *testing.T) {
	freshAccount := quota.Account{
		Provider: "codex", Name: "fresh", Status: "ok",
		Windows: []quota.Window{{ID: "5h", Label: "5h", RemainingPercent: quota.Percent(80), Source: "test"}},
	}
	fetcher := &sequenceFetcher{results: []fetchResult{
		{accounts: []quota.Account{freshAccount}},
		{accounts: []quota.Account{freshAccount}},
	}}
	usageFetcher := &stubUsageFetcher{usage: &quota.Usage{TodayTokens: 1000, TodayCost: 1.5, Currency: "$", Source: "test"}}
	service := New(fetcher, time.Minute)
	service.SetUsageFetcher(usageFetcher)

	if err := service.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	snapshot, ok := service.Snapshot()
	if !ok {
		t.Fatal("service did not become ready")
	}
	if snapshot.Usage == nil || snapshot.Usage.TodayTokens != 1000 {
		t.Fatalf("snapshot.Usage = %+v, want TodayTokens 1000", snapshot.Usage)
	}

	usageFetcher.usage = nil
	usageFetcher.err = errors.New("collector down")
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh() after collector failure error = %v", err)
	}
	snapshot, _ = service.Snapshot()
	if snapshot.Usage == nil || snapshot.Usage.TodayTokens != 1000 {
		t.Fatalf("snapshot.Usage after collector failure = %+v, want retained value", snapshot.Usage)
	}
}

func TestRefreshObserverSeesSuccessAndFailureWithIsolatedSnapshots(t *testing.T) {
	upstreamErr := errors.New("upstream unavailable")
	remaining := 80.0
	fetcher := &sequenceFetcher{results: []fetchResult{
		{accounts: []quota.Account{{
			Provider: "codex", Name: "account", Status: "ok",
			Windows: []quota.Window{{ID: "5h", RemainingPercent: &remaining}},
		}}},
		{err: upstreamErr},
		{accounts: []quota.Account{{
			Provider: "codex", Name: "account", Status: "ok",
			Windows: []quota.Window{{ID: "5h", RemainingPercent: &remaining}},
		}}},
	}}
	service := New(fetcher, time.Minute)
	type refreshEvent struct {
		snapshot   quota.Snapshot
		successful bool
	}
	var observed []refreshEvent
	service.SetRefreshObserver(func(snapshot quota.Snapshot, successful bool) {
		observed = append(observed, refreshEvent{snapshot: snapshot, successful: successful})
		if len(snapshot.Accounts) > 0 {
			snapshot.Accounts[0].Name = "observer mutation"
		}
	})

	if err := service.Refresh(context.Background()); err != nil {
		t.Fatalf("first Refresh() error = %v", err)
	}
	if err := service.Refresh(context.Background()); !errors.Is(err, upstreamErr) {
		t.Fatalf("failed Refresh() error = %v, want %v", err, upstreamErr)
	}
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatalf("third Refresh() error = %v", err)
	}
	if len(observed) != 3 {
		t.Fatalf("refresh observer calls = %d, want one event per completed refresh", len(observed))
	}
	if !observed[0].successful || observed[1].successful || !observed[2].successful {
		t.Fatalf("refresh success sequence = %v/%v/%v, want true/false/true", observed[0].successful, observed[1].successful, observed[2].successful)
	}
	if !observed[1].snapshot.Stale || len(observed[1].snapshot.Errors) != 1 || observed[1].snapshot.Errors[0] != upstreamErr.Error() {
		t.Fatalf("failed refresh event = %#v, want isolated stale/error snapshot", observed[1].snapshot)
	}
	snapshot, ready := service.Snapshot()
	if !ready || len(snapshot.Accounts) != 1 || snapshot.Accounts[0].Name != "account" {
		t.Fatalf("observer mutated service snapshot: ready=%v snapshot=%#v", ready, snapshot)
	}
}
