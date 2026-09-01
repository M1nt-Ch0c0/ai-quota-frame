package cliproxy

import (
	"math"
	"testing"
	"time"
)

func TestNumberRejectsNonFiniteValues(t *testing.T) {
	for _, value := range []any{"NaN", "+Inf", math.Inf(-1), math.NaN()} {
		if parsed, ok := number(value); ok {
			t.Fatalf("number(%v) = %v, true; want rejected", value, parsed)
		}
	}
}

func TestPassiveWindowsNormalizeProviderUnitsFromFixture(t *testing.T) {
	now := time.Date(2026, time.August, 30, 10, 10, 0, 0, time.UTC)
	tests := []struct {
		name     string
		provider string
		entryKey string
	}{
		{name: "Codex percent stays percent", provider: "codex", entryKey: "codex"},
		{name: "Claude fraction becomes percent", provider: "claude", entryKey: "claude"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := providerFixtureMap(t, "passive_headers.json")
			entry := mapValue(fixture[test.entryKey])
			windows := passiveWindows(test.provider, entry, now, time.Hour)
			if len(windows) != 1 {
				t.Fatalf("window count = %d, want 1", len(windows))
			}
			window := windows[0]
			assertProviderPercent(t, window.UsedPercent, 42, "passive used")
			assertProviderPercent(t, window.RemainingPercent, 58, "passive remaining")
			assertProviderReset(t, window.ResetsAt, "2026-08-30T10:30:00Z")
			if window.ObservedAt == nil || !window.ObservedAt.Equal(time.Date(2026, time.August, 30, 10, 0, 0, 0, time.UTC)) {
				t.Fatalf("observed_at = %v, want 2026-08-30T10:00:00Z", window.ObservedAt)
			}
			if window.Source != passiveQuotaSource {
				t.Fatalf("source = %q, want %q", window.Source, passiveQuotaSource)
			}
		})
	}
}

func TestPassiveWindowsRequireFreshObservation(t *testing.T) {
	now := time.Date(2026, time.August, 30, 10, 10, 0, 0, time.UTC)
	tests := []struct {
		name       string
		observedAt string
		wantWindow bool
	}{
		{name: "fresh", observedAt: "2026-08-30T10:00:00Z", wantWindow: true},
		{name: "expired", observedAt: "2026-08-30T08:00:00Z", wantWindow: false},
		{name: "missing", observedAt: "", wantWindow: false},
		{name: "more than five minutes in future", observedAt: "2026-08-30T10:16:00Z", wantWindow: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := providerFixtureMap(t, "passive_headers.json")
			entry := mapValue(fixture["claude"])
			quotaPayload := mapValue(entry["quota"])
			if test.observedAt == "" {
				delete(quotaPayload, "observed_at")
			} else {
				quotaPayload["observed_at"] = test.observedAt
			}

			windows := passiveWindows("claude", entry, now, time.Hour)
			if got := len(windows) > 0; got != test.wantWindow {
				t.Fatalf("has windows = %t, want %t; windows = %+v", got, test.wantWindow, windows)
			}
		})
	}
}
