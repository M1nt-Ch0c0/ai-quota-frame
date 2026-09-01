package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ListenAddr       string
	CLIProxyBaseURL  string
	ManagementKey    string
	FrameAccessToken string
	CPAMPBaseURL     string
	CPAMPAdminKey    string
	RefreshInterval  time.Duration
	PassiveMaxAge    time.Duration
	RequestTimeout   time.Duration
	MaxConcurrency   int
	Location         *time.Location
	DemoMode         bool
	AllowNoToken     bool
}

func FromEnv() (Config, error) {
	refreshInterval, err := envDuration("REFRESH_INTERVAL", 5*time.Minute)
	if err != nil {
		return Config{}, err
	}
	requestTimeout, err := envDuration("REQUEST_TIMEOUT", 20*time.Second)
	if err != nil {
		return Config{}, err
	}
	passiveMaxAge, err := envDuration("PASSIVE_MAX_AGE", 24*time.Hour)
	if err != nil {
		return Config{}, err
	}
	maxConcurrency, err := envInt("MAX_CONCURRENCY", 4)
	if err != nil {
		return Config{}, err
	}
	if maxConcurrency < 1 || maxConcurrency > 32 {
		return Config{}, errors.New("MAX_CONCURRENCY must be between 1 and 32")
	}
	demoMode, err := envBool("DEMO_MODE", false)
	if err != nil {
		return Config{}, err
	}
	allowNoToken, err := envBool("ALLOW_INSECURE_NO_TOKEN", false)
	if err != nil {
		return Config{}, err
	}

	timezone := envString("TZ", "Asia/Shanghai")
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return Config{}, fmt.Errorf("load TZ %q: %w", timezone, err)
	}

	config := Config{
		ListenAddr:       envString("LISTEN_ADDR", ":8787"),
		CLIProxyBaseURL:  strings.TrimRight(envString("CLIPROXY_BASE_URL", "http://127.0.0.1:8317"), "/"),
		ManagementKey:    strings.TrimSpace(os.Getenv("CLIPROXY_MANAGEMENT_KEY")),
		FrameAccessToken: strings.TrimSpace(os.Getenv("FRAME_ACCESS_TOKEN")),
		RefreshInterval:  refreshInterval,
		PassiveMaxAge:    passiveMaxAge,
		RequestTimeout:   requestTimeout,
		MaxConcurrency:   maxConcurrency,
		Location:         location,
		DemoMode:         demoMode,
		AllowNoToken:     allowNoToken,
		CPAMPBaseURL:     strings.TrimRight(envString("CPAMP_BASE_URL", ""), "/"),
		CPAMPAdminKey:    strings.TrimSpace(os.Getenv("CPAMP_ADMIN_KEY")),
	}

	if config.RefreshInterval < time.Minute {
		return Config{}, errors.New("REFRESH_INTERVAL must be at least 1m")
	}
	if config.RequestTimeout < time.Second || config.RequestTimeout > 2*time.Minute {
		return Config{}, errors.New("REQUEST_TIMEOUT must be between 1s and 2m")
	}
	if config.PassiveMaxAge < 0 {
		return Config{}, errors.New("PASSIVE_MAX_AGE must not be negative")
	}
	if !config.DemoMode && config.ManagementKey == "" {
		return Config{}, errors.New("CLIPROXY_MANAGEMENT_KEY is required")
	}
	if config.FrameAccessToken == "" && !config.AllowNoToken {
		return Config{}, errors.New("FRAME_ACCESS_TOKEN is required; set ALLOW_INSECURE_NO_TOKEN=true only for isolated development")
	}
	if !config.DemoMode && config.FrameAccessToken != "" && config.FrameAccessToken == config.ManagementKey {
		return Config{}, errors.New("FRAME_ACCESS_TOKEN must be different from CLIPROXY_MANAGEMENT_KEY")
	}
	if config.AllowNoToken && config.FrameAccessToken == "" && !isLoopbackListenAddress(config.ListenAddr) {
		return Config{}, errors.New("ALLOW_INSECURE_NO_TOKEN requires LISTEN_ADDR to use an explicit loopback host")
	}
	if config.CPAMPAdminKey != "" && config.CPAMPBaseURL == "" {
		config.CPAMPBaseURL = "http://127.0.0.1:18317"
	}
	if config.CPAMPBaseURL != "" && config.CPAMPAdminKey == "" {
		return Config{}, errors.New("CPAMP_ADMIN_KEY is required when CPAMP_BASE_URL is set")
	}
	return config, nil
}

func isLoopbackListenAddress(address string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(address))
	if err != nil {
		return false
	}
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "localhost" {
		return true
	}
	parsed := net.ParseIP(host)
	return parsed != nil && parsed.IsLoopback()
}

func envString(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envDuration(name string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
	}
	return parsed, nil
}

func envInt(name string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
	}
	return parsed, nil
}

func envBool(name string, fallback bool) (bool, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("parse %s: %w", name, err)
	}
	return parsed, nil
}
