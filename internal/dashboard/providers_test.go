package dashboard

import (
	"reflect"
	"testing"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/quota"
)

func TestParseDisplayProvidersDefault(t *testing.T) {
	providers, err := ParseDisplayProviders("")
	if err != nil {
		t.Fatalf("ParseDisplayProviders() error = %v", err)
	}
	if !reflect.DeepEqual(providers, DefaultDisplayProviders()) {
		t.Fatalf("default providers = %#v", providers)
	}
}

func TestParseDisplayProvidersCustomSlots(t *testing.T) {
	providers, err := ParseDisplayProviders("codex, claude:CLAUDE, google")
	if err != nil {
		t.Fatalf("ParseDisplayProviders() error = %v", err)
	}
	want := []DisplayProvider{
		{IDs: []string{"codex"}, Group: "codex", Label: "CODEX"},
		{IDs: []string{"claude"}, Group: "claude", Label: "CLAUDE"},
		{IDs: []string{"gemini-cli", "antigravity"}, Group: "gemini-cli", Label: "GEMINI"},
	}
	if !reflect.DeepEqual(providers, want) {
		t.Fatalf("providers = %#v, want %#v", providers, want)
	}
}

func TestParseDisplayProvidersRejectsTooManyRows(t *testing.T) {
	_, err := ParseDisplayProviders("a,b,c,d,e,f")
	if err == nil {
		t.Fatal("expected too-many-rows error")
	}
}

func TestAggregateRowsUsesConfiguredProviders(t *testing.T) {
	providers, err := ParseDisplayProviders("claude,kimi")
	if err != nil {
		t.Fatalf("ParseDisplayProviders() error = %v", err)
	}
	rows := aggregateRows([]quota.Account{
		{Provider: "claude", Status: "ok", Windows: []quota.Window{{ID: "5h", RemainingPercent: quota.Percent(40)}}},
		{Provider: "codex", Status: "ok", Windows: []quota.Window{{ID: "5h", RemainingPercent: quota.Percent(90)}}},
	}, providers)
	if len(rows) != 2 || rows[0].provider != "CLAUDE" || rows[1].provider != "KIMI" {
		t.Fatalf("rows = %#v, want CLAUDE then KIMI", rows)
	}
	if rows[1].status != "missing" {
		t.Fatalf("KIMI row status = %q, want missing", rows[1].status)
	}
}
