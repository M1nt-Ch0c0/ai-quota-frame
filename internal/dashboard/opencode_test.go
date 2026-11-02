package dashboard

import (
	"strings"
	"testing"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/quota"
)

func TestOpenCodeRowShowsQuotaAndPlan(t *testing.T) {
	providers, err := ParseDisplayProviders("codex,opencode-go,antigravity:AGY")
	if err != nil {
		t.Fatal(err)
	}
	if providers[1].Group != "opencode" || providers[1].Label != "OPENCODE" {
		t.Fatalf("opencode slot = %+v", providers[1])
	}
	snapshot := testSnapshot()
	reset := snapshot.Accounts[0].Windows[0].ResetsAt
	snapshot.Accounts = append(snapshot.Accounts,
		quota.Account{Provider: "opencode", Name: "oc", Plan: "go plus", Status: "ok", Windows: []quota.Window{
			{ID: "1m", Label: "1m", RemainingPercent: quota.Percent(75), Source: "t"},
			{ID: "7d", Label: "7d", RemainingPercent: quota.Percent(99), Source: "t"},
			{ID: "5h", Label: "5h", RemainingPercent: quota.Percent(98), ResetsAt: reset, Source: "t"},
		}},
		quota.Account{Provider: "antigravity", Name: "agy", Plan: "ultra", Status: "ok", Windows: []quota.Window{
			{ID: "5h", Label: "5h", RemainingPercent: quota.Percent(40), Source: "t"},
		}},
	)
	renderer, err := New(time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	renderer.SetDisplayProviders(providers)
	html, err := renderer.renderHTML(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"OPENCODE", "Go Plus", "98%", "99%", "75%", ">1m<", `windows-3`, `data-logo="opencode"`, "AGY", "Ultra"} {
		if !strings.Contains(html, needle) {
			t.Errorf("frame lacks %q", needle)
		}
	}
}

func TestDisplayPlanNewTiers(t *testing.T) {
	for raw, want := range map[string]string{"go": "Go", "go plus": "Go Plus", "ultra": "Ultra", "standard": "Standard", "pro": "Pro"} {
		if got := displayPlan(raw); got != want {
			t.Errorf("displayPlan(%q) = %q, want %q", raw, got, want)
		}
	}
}
