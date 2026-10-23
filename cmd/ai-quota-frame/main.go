package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"syscall"
	_ "time/tzdata"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/ble"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/config"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/console"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/dashboard"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/frame"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/push"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/quota"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/service"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/sources"
)

func main() {
	os.Exit(run())
}

func run() int {
	if len(os.Args) > 1 && os.Args[1] == "console" {
		return runConsole(os.Args[2:], os.Stdout, os.Stderr)
	}
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
	directory, directoryErr := console.Directory()
	var bridge *consoleBridge
	if directoryErr == nil && runtime.GOOS == "darwin" && configuration.PhotoFrameTransport == "ble" {
		state := console.NewState(configuration.RefreshInterval, configuration.FrameTheme, configuration.PhotoFrameTransport, configuration.PhotoFrameDeviceID, directory)
		bridge = newConsoleBridge(state, configuration.PhotoFrameDeviceID, configuration.PhotoFramePushToken, photoFramePusher.SetPaused, quotaService.Refresh)
		photoFramePusher.SetPaused(!state.Snapshot().Automatic)
		photoFramePusher.SetObserver(state.Stage)
	}
	if configuration.PhotoFrameTransport == "ble" {
		sender, err := ble.NewAppImageSender(configuration.PhotoFrameDeviceID, configuration.PhotoFramePushToken, "photoframe")
		if err != nil {
			logger.Error("initialize BLE image transport", "error", err)
			return 2
		}
		photoFramePusher.SetSender(func(ctx context.Context, payload []byte) error {
			if bridge == nil {
				return sender.Send(ctx, payload, nil)
			}
			if err := bridge.acquire(ctx); err != nil {
				return err
			}
			defer bridge.release()
			bridge.state.Stage("connecting")
			err := sender.Send(ctx, payload, bridge.state.Progress)
			if err == nil {
				bridge.state.Complete(payload)
			}
			if errors.Is(err, ble.ErrImagePending) {
				bridge.state.Pending()
			}
			if errors.Is(err, ble.ErrImageOutcomeUnknown) {
				bridge.state.Unknown()
			}
			if err != nil {
				bridge.state.Failure(consoleTransferError(err))
			}
			return err
		})
	} else if configuration.PhotoFrameDeviceID != "" {
		photoFramePusher.SetDiscovery(func(ctx context.Context) (string, error) {
			return ble.DiscoverPushURL(ctx, configuration.PhotoFrameDeviceID, configuration.PhotoFramePushToken)
		})
	}
	quotaService.SetRefreshObserver(func(snapshot quota.Snapshot, successful bool) {
		if bridge != nil {
			bridge.state.Fetch(successful, snapshot.Stale, snapshot.RefreshedAt, snapshot.NextRefreshAt, len(snapshot.Accounts), len(snapshot.Errors))
		}
		photoFramePusher.ObserveRefresh(snapshot, successful)
	})
	logger.Info("PhotoPainter active push enabled", "transport", configuration.PhotoFrameTransport)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var backgroundWorkers sync.WaitGroup
	if bridge != nil {
		backgroundWorkers.Add(2)
		go func() { defer backgroundWorkers.Done(); bridge.run(ctx) }()
		go func() {
			defer backgroundWorkers.Done()
			path, err := console.SocketPath()
			if err == nil {
				err = console.Serve(ctx, path, bridge.handle)
			}
			if err != nil {
				logger.Warn("local console unavailable")
			}
		}()
	}
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
