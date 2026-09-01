package usage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestFetchUsageReadsDashboardSummary(t *testing.T) {
	location := time.FixedZone("UTC+8", 8*60*60)
	var wantStart int64
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != summaryPath {
			t.Errorf("path = %q, want %q", request.URL.Path, summaryPath)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer fixture-admin-key" {
			t.Errorf("Authorization = %q", got)
		}
		raw := request.URL.Query().Get("today_start_ms")
		start, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || start != wantStart {
			t.Errorf("today_start_ms = %q, want %d", raw, wantStart)
		}
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(map[string]any{
			"today": map[string]any{"total_tokens": 61635795, "total_cost": 36.89079273},
		})
	}))
	defer server.Close()

	now := time.Now().In(location)
	wantStart = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location).UnixMilli()

	client, err := NewClient(server.URL, "fixture-admin-key", 2*time.Second, location)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	usage, err := client.FetchUsage(context.Background())
	if err != nil {
		t.Fatalf("FetchUsage() error = %v", err)
	}
	if usage.TodayTokens != 61635795 {
		t.Fatalf("TodayTokens = %d, want 61635795", usage.TodayTokens)
	}
	if usage.TodayCost < 36.89 || usage.TodayCost > 36.90 {
		t.Fatalf("TodayCost = %v, want ~36.89", usage.TodayCost)
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
