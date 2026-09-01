package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/cliproxy"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/config"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/dashboard"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/httpapi"
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
	api := httpapi.New(quotaService, renderer, configuration.FrameAccessToken, configuration.AllowNoToken)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var quotaWorkers sync.WaitGroup
	quotaWorkers.Add(1)
	go func() {
		defer quotaWorkers.Done()
		quotaService.Run(ctx)
	}()

	server := &http.Server{
		Addr:              configuration.ListenAddr,
		Handler:           requestLog(logger, api.Handler()),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("AI quota frame server started", "listen", configuration.ListenAddr, "demo", configuration.DemoMode)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
			return
		}
		serverErrors <- nil
	}()

	var listenErr error
	select {
	case <-ctx.Done():
	case listenErr = <-serverErrors:
		if listenErr != nil {
			logger.Error("HTTP server failed", "error", listenErr)
		}
		stop()
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP shutdown failed", "error", err)
	}
	quotaWorkers.Wait()
	if listenErr != nil {
		return 1
	}
	return 0
}

func requestLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: response, status: http.StatusOK}
		next.ServeHTTP(recorder, request)
		logger.Info("HTTP request",
			"method", request.Method,
			"path", request.URL.Path,
			"status", recorder.status,
			"duration_ms", time.Since(started).Milliseconds(),
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (recorder *statusRecorder) WriteHeader(status int) {
	recorder.status = status
	recorder.ResponseWriter.WriteHeader(status)
}
