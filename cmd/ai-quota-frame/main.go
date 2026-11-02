package main

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

const usage = `usage: ai-quota-frame <command> [arguments]

Commands:
  daemon        run the collector, renderer and PhotoPainter push service
  ble           pair with, inspect and manage a PhotoPainter over Bluetooth
  console       query the running daemon through the local console socket
  source-check  check the configured quota and usage sources once
  help          show this help

Running without a command only prints this help; the service starts with
"ai-quota-frame daemon". Run "ai-quota-frame <command>" for its own usage.
`

// The service is an explicit subcommand, so a bare invocation never connects
// to data sources or the display.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stdout, usage)
		return 0
	}
	switch args[0] {
	case "daemon":
		return runDaemon(args[1:], stdout, stderr)
	case "ble":
		return runBLE(args[1:], stdout, stderr)
	case "console":
		return runConsole(args[1:], stdout, stderr)
	case "source-check":
		return runSourceCheck(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

func runDaemon(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(stdout, "usage: ai-quota-frame daemon\n\nConfiguration is read from the environment (see .env.example).")
		return 0
	}
	if len(args) > 0 {
		fmt.Fprintln(stderr, "usage: ai-quota-frame daemon (configuration is read from the environment)")
		return 2
	}
	logger := slog.New(slog.NewTextHandler(stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
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
		if bridge != nil {
			bridge.sender = sender
			sender.SetTransportObserver(bridge.state.TransportSelected)
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
