package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/config"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/sources"
)

// source-check uses the normal private launch environment, but never starts the
// renderer, device sender or console. It reports no account names, credentials,
// API response bodies or quota values. --static tests an existing static-key
// configuration without changing the private configuration file.
func runSourceCheck(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("source-check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	static := flags.Bool("static", false, "test configured management keys instead of the Google gateway")
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return 2
	}
	if *static {
		// Resolve the diagnostic override before config validation, so a
		// retained service-account file cannot make static settings ambiguous.
		previous, existed := os.LookupEnv("SOURCE_AUTH_MODE")
		_ = os.Setenv("SOURCE_AUTH_MODE", "management-key")
		defer func() {
			if existed {
				_ = os.Setenv("SOURCE_AUTH_MODE", previous)
			} else {
				_ = os.Unsetenv("SOURCE_AUTH_MODE")
			}
		}()
	}
	c, err := config.FromEnv()
	if err != nil {
		fmt.Fprintln(stderr, "source-check: configuration invalid; check required settings")
		return 2
	}
	mode := c.SourceAuthMode
	report := map[string]any{
		"auth_mode":                 mode,
		"management_key_configured": c.ManagementKey != "",
		"usage_key_configured":      c.CPAMPAdminKey != "",
		"usage_configured":          c.CPAMPBaseURL != "",
		"observed_at":               time.Now().UTC(),
	}
	if *static {
		if c.ManagementKey == "" || (c.CPAMPBaseURL != "" && c.CPAMPAdminKey == "") {
			report["error"] = "static management credentials are not configured"
			_ = json.NewEncoder(stdout).Encode(report)
			return 1
		}
	}
	clients, err := sources.New(c)
	if err != nil {
		report["error"] = "source client initialization failed"
		_ = json.NewEncoder(stdout).Encode(report)
		return 1
	}
	clients.Quota.SetMaxConcurrency(c.MaxConcurrency)
	clients.Quota.SetPassiveMaxAge(c.PassiveMaxAge)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	start := time.Now()
	accounts, warnings, quotaErr := clients.Quota.Fetch(ctx)
	report["quota_ok"] = quotaErr == nil
	if quotaErr != nil {
		// These clients intentionally exclude response bodies and credentials
		// from all errors. Do not substitute generic HTTP client errors here.
		report["quota_error"] = quotaErr.Error()
	} else {
		report["account_count"] = len(accounts)
		report["warning_count"] = len(warnings)
		counts := make(map[string]int)
		issues := make([]map[string]string, 0)
		freshAccounts := 0
		for _, account := range accounts {
			counts[account.Provider+":"+account.Status]++
			if !account.Stale && account.Error == "" && account.Warning == "" && account.Status != "disabled" {
				for _, window := range account.Windows {
					if window.UsedPercent != nil || window.RemainingPercent != nil {
						freshAccounts++
						break
					}
				}
			}
			if account.Error != "" || account.Warning != "" {
				issues = append(issues, map[string]string{"provider": account.Provider, "error": account.Error, "warning": account.Warning})
			}
		}
		report["account_states"] = counts
		report["account_issues"] = issues
		report["fresh_account_count"] = freshAccounts
		report["quota_fresh"] = freshAccounts > 0
	}
	usageOK := true
	if clients.Usage != nil {
		_, usageErr := clients.Usage.FetchUsage(ctx)
		usageOK = usageErr == nil
		report["usage_ok"] = usageOK
		if usageErr != nil {
			report["usage_error"] = usageErr.Error()
		}
	}
	report["elapsed_ms"] = time.Since(start).Milliseconds()
	if json.NewEncoder(stdout).Encode(report) != nil || quotaErr != nil || !usageOK {
		return 1
	}
	return 0
}
