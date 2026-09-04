package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	_ "time/tzdata"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/cliproxy"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/config"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/dashboard"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/frame"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/push"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/service"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/usage"
)

func main() {
	os.Exit(run())
}

func run() int {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	configuration, err := config.FromEnv()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		return 2
	}
	if err := dashboard.CheckChrome(); err != nil {
		logger.Error("Chrome preflight failed", "error", err)
		return 2
	}

	var fetcher service.Fetcher
	if configuration.DemoMode {
		logger.Warn("demo mode is enabled; no CLIProxyAPI data will be queried")
		fetcher = service.DemoFetcher{}
	} else {
		client, clientErr := cliproxy.NewClient(
			configuration.CLIProxyBaseURL,
			configuration.ManagementKey,
			configuration.RequestTimeout,
		)
		if clientErr != nil {
			logger.Error("initialize CLIProxyAPI client", "error", clientErr)
			return 2
		}
		client.SetMaxConcurrency(configuration.MaxConcurrency)
		client.SetPassiveMaxAge(configuration.PassiveMaxAge)
		fetcher = client
	}
	quotaService := service.New(fetcher, configuration.RefreshInterval)
	if configuration.DemoMode {
		quotaService.SetUsageFetcher(service.DemoUsageFetcher{})
	} else if configuration.CPAMPBaseURL != "" {
		usageClient, usageErr := usage.NewClient(
			configuration.CPAMPBaseURL,
			configuration.CPAMPAdminKey,
			configuration.RequestTimeout,
			configuration.Location,
		)
		if usageErr != nil {
			logger.Error("initialize usage collector client", "error", usageErr)
			return 2
		}
		quotaService.SetUsageFetcher(usageClient)
	} else {
		logger.Info("usage stats disabled; set CPAMP_BASE_URL and CPAMP_ADMIN_KEY to show today's tokens")
	}
	renderer, err := dashboard.New(configuration.Location)
	if err != nil {
		logger.Error("initialize renderer", "error", err)
		return 2
	}
	renderer.SetDisplayProviders(configuration.DisplayProviders)
	frameProducer := frame.NewProducer(renderer)
	photoFramePusher := push.New(
		configuration.PhotoFramePushURL,
		configuration.PhotoFramePushToken,
		frameProducer,
		logger,
	)
	quotaService.SetRefreshObserver(photoFramePusher.ObserveRefresh)
	logger.Info("PhotoPainter active push enabled")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var backgroundWorkers sync.WaitGroup
	backgroundWorkers.Add(1)
	go func() {
		defer backgroundWorkers.Done()
		photoFramePusher.Run(ctx)
	}()
	backgroundWorkers.Add(1)
	go func() {
		defer backgroundWorkers.Done()
		quotaService.Run(ctx)
	}()

	<-ctx.Done()
	backgroundWorkers.Wait()
	return 0
}
