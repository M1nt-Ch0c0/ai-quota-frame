package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/quota"
)

type Fetcher interface {
	Fetch(context.Context) ([]quota.Account, []string, error)
}

// UsageFetcher reports today's token consumption and cost. It is optional:
// without one the snapshot simply carries no usage section.
type UsageFetcher interface {
	FetchUsage(context.Context) (*quota.Usage, error)
}

type Service struct {
	fetcher      Fetcher
	usageFetcher UsageFetcher
	interval     time.Duration

	refreshMu       sync.Mutex
	mu              sync.RWMutex
	snapshot        quota.Snapshot
	fingerprint     [sha256.Size]byte
	hasData         bool
	ready           bool
	refreshObserver func(quota.Snapshot, bool)
	now             func() time.Time
}

// SetUsageFetcher attaches the optional usage collector client. Call it
// before Run; usage failures never fail a refresh, they only retain the
// previous usage section.
func (service *Service) SetUsageFetcher(fetcher UsageFetcher) {
	service.usageFetcher = fetcher
}

// SetRefreshObserver installs a non-blocking callback invoked with an isolated
// snapshot after every completed refresh. successful is false when the
// upstream refresh failed; sinks can use that event to discard obsolete work
// without publishing the failed snapshot.
func (service *Service) SetRefreshObserver(observer func(quota.Snapshot, bool)) {
	service.mu.Lock()
	service.refreshObserver = observer
	service.mu.Unlock()
}

func New(fetcher Fetcher, interval time.Duration) *Service {
	return &Service{
		fetcher:  fetcher,
		interval: interval,
		now:      time.Now,
	}
}

func (service *Service) Run(ctx context.Context) {
	for {
		_ = service.Refresh(ctx)
		if ctx.Err() != nil {
			return
		}
		timer := time.NewTimer(service.interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-timer.C:
		}
	}
}

func (service *Service) Refresh(ctx context.Context) error {
	service.refreshMu.Lock()
	defer service.refreshMu.Unlock()

	accounts, fetchErrors, err := service.fetcher.Fetch(ctx)
	if (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) && ctx.Err() != nil {
		return err
	}
	usage := service.fetchUsage(ctx)
	now := service.now().UTC()

	service.mu.Lock()

	next := now.Add(service.interval)
	if err != nil {
		message := err.Error()
		if service.hasData {
			service.snapshot.RefreshedAt = now
			service.snapshot.NextRefreshAt = next
			service.snapshot.Stale = true
			service.snapshot.Errors = []string{message}
			if usage != nil {
				service.snapshot.Usage = usage
			}
			service.updateFingerprintLocked(now)
			observer := service.refreshObserver
			failedSnapshot := cloneSnapshot(service.snapshot)
			service.mu.Unlock()
			if observer != nil {
				observer(failedSnapshot, false)
			}
			return err
		}
		service.snapshot = quota.Snapshot{
			SchemaVersion: quota.SchemaVersion,
			RefreshedAt:   now,
			DataUpdatedAt: now,
			NextRefreshAt: next,
			Stale:         true,
			Accounts:      []quota.Account{},
			Errors:        []string{message},
		}
		service.hasData = true
		service.fingerprint = semanticFingerprint(service.snapshot)
		observer := service.refreshObserver
		failedSnapshot := cloneSnapshot(service.snapshot)
		service.mu.Unlock()
		if observer != nil {
			observer(failedSnapshot, false)
		}
		return err
	}

	previousUpdatedAt := service.snapshot.DataUpdatedAt
	previousUsage := service.snapshot.Usage
	lastFreshAt := cloneTime(service.snapshot.LastFreshAt)
	stale := allQuotaUnavailable(accounts)
	if !stale {
		lastFreshAt = timePointer(now)
	}
	if service.hasData {
		accounts = retainBestKnownAccountWindows(accounts, service.snapshot.Accounts)
	}
	service.snapshot = quota.Snapshot{
		SchemaVersion: quota.SchemaVersion,
		RefreshedAt:   now,
		DataUpdatedAt: now,
		LastFreshAt:   lastFreshAt,
		NextRefreshAt: next,
		Stale:         stale,
		Accounts:      accounts,
		Usage:         usageOrPrevious(usage, previousUsage),
		Errors:        fetchErrors,
	}
	nextFingerprint := semanticFingerprint(service.snapshot)
	if service.hasData && nextFingerprint == service.fingerprint && !previousUpdatedAt.IsZero() {
		service.snapshot.DataUpdatedAt = previousUpdatedAt
	}
	service.fingerprint = nextFingerprint
	service.hasData = true
	service.ready = true
	observer := service.refreshObserver
	readySnapshot := cloneSnapshot(service.snapshot)
	service.mu.Unlock()
	if observer != nil {
		observer(readySnapshot, true)
	}
	return nil
}

func (service *Service) updateFingerprintLocked(now time.Time) {
	nextFingerprint := semanticFingerprint(service.snapshot)
	if nextFingerprint != service.fingerprint {
		service.snapshot.DataUpdatedAt = now
		service.fingerprint = nextFingerprint
	}
}

func (service *Service) fetchUsage(ctx context.Context) *quota.Usage {
	if service.usageFetcher == nil {
		return nil
	}
	usage, err := service.usageFetcher.FetchUsage(ctx)
	if err != nil {
		return nil
	}
	return usage
}

func usageOrPrevious(current, previous *quota.Usage) *quota.Usage {
	if current != nil {
		return cloneUsage(current)
	}
	return cloneUsage(previous)
}

func (service *Service) Snapshot() (quota.Snapshot, bool) {
	service.mu.RLock()
	defer service.mu.RUnlock()
	if !service.hasData {
		return quota.Snapshot{}, false
	}
	return cloneSnapshot(service.snapshot), service.ready
}

func cloneSnapshot(snapshot quota.Snapshot) quota.Snapshot {
	copySnapshot := snapshot
	copySnapshot.LastFreshAt = cloneTime(snapshot.LastFreshAt)
	copySnapshot.Accounts = cloneAccounts(snapshot.Accounts)
	copySnapshot.Errors = append([]string(nil), snapshot.Errors...)
	copySnapshot.Usage = cloneUsage(snapshot.Usage)
	return copySnapshot
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func timePointer(value time.Time) *time.Time {
	copyValue := value.UTC()
	return &copyValue
}

func allQuotaUnavailable(accounts []quota.Account) bool {
	for _, account := range accounts {
		if account.Status == "disabled" {
			continue
		}
		if account.Error == "" && account.Warning == "" && !account.Stale && hasQuotaValue(account.Windows) {
			return false
		}
	}
	return true
}

func hasQuotaValue(windows []quota.Window) bool {
	for _, window := range windows {
		if window.UsedPercent != nil || window.RemainingPercent != nil {
			return true
		}
	}
	return false
}

func retainBestKnownAccountWindows(current, previous []quota.Account) []quota.Account {
	previousByIdentity := make(map[string]quota.Account, len(previous))
	for _, account := range previous {
		previousByIdentity[retentionKey(account)] = account
	}
	result := cloneAccounts(current)
	for index := range result {
		account := &result[index]
		old, ok := previousByIdentity[retentionKey(*account)]
		if !ok || len(old.Windows) == 0 {
			continue
		}
		retainPrevious := account.Error != "" && len(account.Windows) == 0
		if account.Stale && len(account.Windows) > 0 && accountObservationAfter(old, *account) {
			retainPrevious = true
		}
		if !retainPrevious {
			if account.LastFreshAt == nil {
				account.LastFreshAt = cloneTime(old.LastFreshAt)
			}
			continue
		}
		account.Windows = cloneWindows(old.Windows)
		account.LastFreshAt = cloneTime(old.LastFreshAt)
		account.Stale = true
		if account.Error != "" {
			account.Status = quota.StatusForWindows(account.Windows)
			account.Warning = "refresh failed; showing the previous successful quota snapshot"
			account.Error = ""
		} else {
			account.Status = quota.StatusForWindows(account.Windows)
			account.Warning = "active refresh failed; showing a newer previous quota snapshot"
		}
	}
	return result
}

func accountObservationAfter(left, right quota.Account) bool {
	leftTime := newestAccountObservation(left)
	rightTime := newestAccountObservation(right)
	return leftTime != nil && (rightTime == nil || leftTime.After(*rightTime))
}

func newestAccountObservation(account quota.Account) *time.Time {
	newest := cloneTime(account.LastFreshAt)
	for _, window := range account.Windows {
		if window.ObservedAt != nil && (newest == nil || window.ObservedAt.After(*newest)) {
			newest = cloneTime(window.ObservedAt)
		}
	}
	return newest
}

func retentionKey(account quota.Account) string {
	if account.RetentionID != "" {
		return account.Provider + "\x00id\x00" + account.RetentionID
	}
	return account.Provider + "\x00name\x00" + account.Name
}

func cloneUsage(usage *quota.Usage) *quota.Usage {
	if usage == nil {
		return nil
	}
	copyUsage := *usage
	copyUsage.Days = append([]quota.UsageDay(nil), usage.Days...)
	return &copyUsage
}

func cloneAccounts(accounts []quota.Account) []quota.Account {
	result := append([]quota.Account(nil), accounts...)
	for index := range result {
		result[index].LastFreshAt = cloneTime(accounts[index].LastFreshAt)
		result[index].Windows = cloneWindows(accounts[index].Windows)
	}
	return result
}

func cloneWindows(windows []quota.Window) []quota.Window {
	result := append([]quota.Window(nil), windows...)
	for index := range result {
		if windows[index].UsedPercent != nil {
			value := *windows[index].UsedPercent
			result[index].UsedPercent = &value
		}
		if windows[index].RemainingPercent != nil {
			value := *windows[index].RemainingPercent
			result[index].RemainingPercent = &value
		}
		if windows[index].ResetsAt != nil {
			value := *windows[index].ResetsAt
			result[index].ResetsAt = &value
		}
		if windows[index].ObservedAt != nil {
			value := *windows[index].ObservedAt
			result[index].ObservedAt = &value
		}
	}
	return result
}

func semanticFingerprint(snapshot quota.Snapshot) [sha256.Size]byte {
	accounts := cloneAccounts(snapshot.Accounts)
	for accountIndex := range accounts {
		accounts[accountIndex].Name = ""
		accounts[accountIndex].LastFreshAt = nil
		for windowIndex := range accounts[accountIndex].Windows {
			window := &accounts[accountIndex].Windows[windowIndex]
			window.UsedPercent = nil
			window.ObservedAt = nil
			if window.Source != "demo" {
				window.Source = ""
			}
			if window.ResetsAt != nil {
				value := window.ResetsAt.UTC().Truncate(time.Minute)
				window.ResetsAt = &value
			}
		}
	}
	usage := cloneUsage(snapshot.Usage)
	if usage != nil && usage.Source != "demo" {
		usage.Source = ""
	}
	payload := struct {
		Stale    bool            `json:"stale"`
		Accounts []quota.Account `json:"accounts"`
		Usage    *quota.Usage    `json:"usage,omitempty"`
		Errors   []string        `json:"errors,omitempty"`
	}{
		Stale:    snapshot.Stale,
		Accounts: accounts,
		Usage:    usage,
		Errors:   snapshot.Errors,
	}
	encoded, _ := json.Marshal(payload)
	return sha256.Sum256(encoded)
}
