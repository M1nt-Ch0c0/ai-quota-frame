// Package webframe is the browser-facing adapter for the existing native
// collector and renderer. It contains no parallel quota or layout rules.
package webframe

import (
	"context"
	"errors"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/cliproxy"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/dashboard"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/quota"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/service"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/usage"
)

type Options struct {
	Theme          string `json:"theme"`
	Providers      string `json:"providers"`
	Timezone       string `json:"timezone"`
	RefreshSeconds int    `json:"refreshSeconds"`
}

func (options Options) normalized() (Options, *time.Location, error) {
	if options.Theme == "" {
		options.Theme = "mint-skin"
	}
	if options.Timezone == "" {
		options.Timezone = "Asia/Shanghai"
	}
	if options.Providers == "" {
		options.Providers = "claude:CLAUDE,codex:CODEX,xai:GROK"
	}
	if options.RefreshSeconds == 0 {
		options.RefreshSeconds = 900
	}
	if options.RefreshSeconds < 60 || options.RefreshSeconds > 86400 {
		return options, nil, errors.New("刷新间隔应为 1 分钟至 24 小时")
	}
	if _, err := dashboard.ParseTheme(options.Theme); err != nil {
		return options, nil, err
	}
	if _, err := dashboard.ParseDisplayProviders(options.Providers); err != nil {
		return options, nil, err
	}
	location, err := time.LoadLocation(options.Timezone)
	if err != nil {
		return options, nil, errors.New("时区无效")
	}
	return options, location, nil
}

type Collector struct{ service *service.Service }

// NewCollector uses the same active provider adapters, passive fallback and
// seven-day usage queries as the native service. The host origin is supplied
// by the browser bridge, never inferred from auth metadata or a remote reply.
func NewCollector(origin, managementKey string, options Options) (*Collector, error) {
	options, location, err := options.normalized()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(managementKey) == "" || len(managementKey) > 4096 || strings.ContainsAny(managementKey, "\r\n") {
		return nil, errors.New("请输入有效的 CPM 管理密钥")
	}
	quotaClient, err := cliproxy.NewClient(origin, managementKey, 20*time.Second)
	if err != nil {
		return nil, err
	}
	quotaClient.SetMaxConcurrency(4)
	quotaClient.SetPassiveMaxAge(24 * time.Hour)
	usageClient, err := usage.NewClient(origin, managementKey, 20*time.Second, location)
	if err != nil {
		return nil, err
	}
	svc := service.New(quotaClient, time.Duration(options.RefreshSeconds)*time.Second)
	svc.SetUsageFetcher(usageClient)
	return &Collector{service: svc}, nil
}

func (collector *Collector) Collect(ctx context.Context) (quota.Snapshot, error) {
	err := collector.service.Refresh(ctx)
	snapshot, ready := collector.service.Snapshot()
	if err != nil {
		return snapshot, err
	}
	if !ready {
		return snapshot, errors.New("额度尚未就绪")
	}
	return snapshot, nil
}

func HTML(snapshot quota.Snapshot, options Options) (string, error) {
	options, location, err := options.normalized()
	if err != nil {
		return "", err
	}
	if snapshot.SchemaVersion != quota.SchemaVersion || len(snapshot.Accounts) > 1000 {
		return "", errors.New("额度快照格式不兼容")
	}
	if snapshot.DataUpdatedAt.IsZero() {
		snapshot.DataUpdatedAt = snapshot.RefreshedAt
	}
	renderer, err := dashboard.New(location)
	if err != nil {
		return "", err
	}
	providers, err := dashboard.ParseDisplayProviders(options.Providers)
	if err != nil {
		return "", err
	}
	renderer.SetDisplayProviders(providers)
	if err := renderer.SetTheme(options.Theme); err != nil {
		return "", err
	}
	return renderer.RenderHTML(snapshot)
}
