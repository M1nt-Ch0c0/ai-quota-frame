package usage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestFetchUsageReadsSevenDailyWindows(t *testing.T) {
	location := time.FixedZone("UTC+8", 8*60*60)
	now := time.Date(2026, time.September, 1, 21, 0, 0, 0, location)
	today := time.Date(2026, time.September, 1, 0, 0, 0, 0, location)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != summaryPath {
			t.Errorf("path = %q, want %q", request.URL.Path, summaryPath)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer fixture-admin-key" {
			t.Errorf("Authorization = %q", got)
		}
		start, err := strconv.ParseInt(request.URL.Query().Get("today_start_ms"), 10, 64)
		if err != nil {
			t.Errorf("today_start_ms parse error: %v", err)
			return
		}
		end, err := strconv.ParseInt(request.URL.Query().Get("now_ms"), 10, 64)
		if err != nil {
			t.Errorf("now_ms parse error: %v", err)
			return
		}
		day := time.UnixMilli(start).In(location)
		offset := int(day.Sub(today.AddDate(0, 0, -6)) / (24 * time.Hour))
		if offset < 0 || offset > 6 {
			t.Errorf("unexpected window start %s", day)
		}
		if offset == 6 && end != now.UnixMilli() {
			t.Errorf("today now_ms = %d, want %d", end, now.UnixMilli())
		}
		requests.Add(1)
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(map[string]any{
			"today": map[string]any{
				"total_tokens": 1000 * (offset + 1),
				"total_cost":   float64(offset+1) + 0.25,
			},
		})
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "fixture-admin-key", 2*time.Second, location)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	client.now = func() time.Time { return now }

	usage, err := client.FetchUsage(context.Background())
	if err != nil {
		t.Fatalf("FetchUsage() error = %v", err)
	}
	if requests.Load() != 7 {
		t.Fatalf("collector requests = %d, want 7", requests.Load())
	}
	if usage.TodayTokens != 7000 {
		t.Fatalf("TodayTokens = %d, want 7000", usage.TodayTokens)
	}
	if len(usage.Days) != 7 || usage.Days[0].Date != "2026-08-26" || usage.Days[6].Date != "2026-09-01" {
		t.Fatalf("Days = %#v", usage.Days)
	}
	if usage.Days[0].Tokens != 1000 || usage.Days[6].Tokens != 7000 {
		t.Fatalf("day tokens = %#v", usage.Days)
	}
	if usage.Currency != "$" || usage.Source != usageSource {
		t.Fatalf("Currency/Source = %q/%q", usage.Currency, usage.Source)
	}
}

func TestNewClientRejectsPlaintextRemoteCollector(t *testing.T) {
	if _, err := NewClient("http://192.168.1.10:18317", "key", time.Second, time.UTC); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("NewClient() error = %v, want loopback rejection", err)
	}
	if _, err := NewClient("http://127.0.0.1:18317", "", time.Second, time.UTC); err == nil {
		t.Fatal("NewClient() accepted an empty admin key")
	}
}
