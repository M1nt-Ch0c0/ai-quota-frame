package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	_ "time/tzdata"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/ble"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/config"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/dashboard"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/frame"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/push"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/service"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/sources"
)

func main() {
	os.Exit(run())
}

func run() int {
	if len(os.Args) > 1 && os.Args[1] == "ble" {
		return runBLE(os.Args[2:], os.Stdout, os.Stderr)
	}
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
	var clients sources.Clients
	if configuration.DemoMode {
		logger.Warn("demo mode is enabled; no CLIProxyAPI data will be queried")
		fetcher = service.DemoFetcher{}
	} else {
		clients, err = sources.New(configuration)
		if err != nil {
			logger.Error("initialize data source clients", "error", err)
			return 2
		}
		if clients.Identity != "" {
			logger.Info("authenticating management APIs with a Google ID token", "service_account", clients.Identity)
		}
		clients.Quota.SetMaxConcurrency(configuration.MaxConcurrency)
		clients.Quota.SetPassiveMaxAge(configuration.PassiveMaxAge)
		fetcher = clients.Quota
	}
	quotaService := service.New(fetcher, configuration.RefreshInterval)
	if configuration.DemoMode {
		quotaService.SetUsageFetcher(service.DemoUsageFetcher{})
	} else if clients.Usage != nil {
		quotaService.SetUsageFetcher(clients.Usage)
	} else {
		logger.Info("usage stats disabled; set CPAMP_BASE_URL and CPAMP_ADMIN_KEY to show today's tokens")
	}
	renderer, err := dashboard.New(configuration.Location)
	if err != nil {
		logger.Error("initialize renderer", "error", err)
		return 2
	}
	renderer.SetDisplayProviders(configuration.DisplayProviders)
	if err := renderer.SetTheme(configuration.FrameTheme); err != nil {
		logger.Error("select frame theme", "error", err)
		return 2
	}
	frameProducer := frame.NewProducer(renderer)
	photoFramePusher := push.New(
		configuration.PhotoFramePushURL,
		configuration.PhotoFramePushToken,
		frameProducer,
		logger,
	)
	photoFramePusher.SetLastFramePath(configuration.LastFramePath)
	if configuration.PhotoFrameTransport == "ble" {
		sender, err := ble.NewAppImageSender(configuration.PhotoFrameDeviceID, configuration.PhotoFramePushToken, "photoframe")
		if err != nil {
			logger.Error("initialize BLE image transport", "error", err)
			return 2
		}
		photoFramePusher.SetSender(func(ctx context.Context, payload []byte) error {
			return sender.Send(ctx, payload, nil)
		})
	} else if configuration.PhotoFrameDeviceID != "" {
		photoFramePusher.SetDiscovery(func(ctx context.Context) (string, error) {
			return ble.DiscoverPushURL(ctx, configuration.PhotoFrameDeviceID, configuration.PhotoFramePushToken)
		})
	}
	quotaService.SetRefreshObserver(photoFramePusher.ObserveRefresh)
	logger.Info("PhotoPainter active push enabled", "transport", configuration.PhotoFrameTransport)

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
