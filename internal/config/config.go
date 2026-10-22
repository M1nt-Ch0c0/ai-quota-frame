package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/ble"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/dashboard"
)

type Config struct {
	CLIProxyBaseURL     string
	ManagementKey       string
	PhotoFramePushURL   string
	PhotoFramePushToken string
	PhotoFrameDeviceID  string
	PhotoFrameTransport string
	CPAMPBaseURL        string
	CPAMPAdminKey       string
	// GoogleServiceAccountFile replaces both management keys with a Google ID
	// token when the APIs sit behind an OAuth gateway that injects them.
	GoogleServiceAccountFile string
	GoogleIDTokenAudience    string
	DisplayProviders         []dashboard.DisplayProvider
	FrameTheme               string
	LastFramePath            string
	RefreshInterval          time.Duration
	PassiveMaxAge            time.Duration
	RequestTimeout           time.Duration
	MaxConcurrency           int
	Location                 *time.Location
	DemoMode                 bool
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
	timezone := envString("TZ", "Asia/Shanghai")
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return Config{}, fmt.Errorf("load TZ %q: %w", timezone, err)
	}
	providers, err := dashboard.ParseDisplayProviders(os.Getenv("DISPLAY_PROVIDERS"))
	if err != nil {
		return Config{}, err
	}
	theme, err := dashboard.ParseTheme(os.Getenv("FRAME_THEME"))
	if err != nil {
		return Config{}, err
	}

	config := Config{
		CLIProxyBaseURL:     strings.TrimRight(envString("CLIPROXY_BASE_URL", "http://127.0.0.1:8317"), "/"),
		ManagementKey:       strings.TrimSpace(os.Getenv("CLIPROXY_MANAGEMENT_KEY")),
		PhotoFramePushURL:   strings.TrimSpace(os.Getenv("PHOTOFRAME_PUSH_URL")),
		PhotoFramePushToken: os.Getenv("PHOTOFRAME_PUSH_TOKEN"),
		PhotoFrameDeviceID:  strings.TrimSpace(os.Getenv("PHOTOFRAME_DEVICE_ID")),
		PhotoFrameTransport: envString("PHOTOFRAME_PUSH_TRANSPORT", "http"),
		DisplayProviders:    providers,
		FrameTheme:          theme,
		LastFramePath:       strings.TrimSpace(os.Getenv("LAST_FRAME_PATH")),
		RefreshInterval:     refreshInterval,
		PassiveMaxAge:       passiveMaxAge,
		RequestTimeout:      requestTimeout,
		MaxConcurrency:      maxConcurrency,
		Location:            location,
		DemoMode:            demoMode,
		CPAMPBaseURL:        strings.TrimRight(envString("CPAMP_BASE_URL", ""), "/"),
		CPAMPAdminKey:       strings.TrimSpace(os.Getenv("CPAMP_ADMIN_KEY")),

		GoogleServiceAccountFile: strings.TrimSpace(os.Getenv("GOOGLE_SERVICE_ACCOUNT_FILE")),
		GoogleIDTokenAudience:    strings.TrimSpace(os.Getenv("GOOGLE_ID_TOKEN_AUDIENCE")),
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
	googleAuth := config.GoogleServiceAccountFile != ""
	if googleAuth && config.GoogleIDTokenAudience == "" {
		return Config{}, errors.New("GOOGLE_ID_TOKEN_AUDIENCE is required with GOOGLE_SERVICE_ACCOUNT_FILE")
	}
	if !googleAuth && config.GoogleIDTokenAudience != "" {
		return Config{}, errors.New("GOOGLE_ID_TOKEN_AUDIENCE requires GOOGLE_SERVICE_ACCOUNT_FILE")
	}
	if !config.DemoMode && !googleAuth && config.ManagementKey == "" {
		return Config{}, errors.New("CLIPROXY_MANAGEMENT_KEY is required")
	}
	if config.PhotoFrameDeviceID != "" {
		config.PhotoFrameDeviceID, err = ble.NormalizeID(config.PhotoFrameDeviceID)
		if err != nil {
			return Config{}, errors.New("PHOTOFRAME_DEVICE_ID must be a 12-digit hexadecimal device ID")
		}
	} else {
		config.PhotoFrameDeviceID, err = ble.LoadBinding()
		if err != nil {
			return Config{}, err
		}
	}
	if (config.PhotoFramePushURL == "" && config.PhotoFrameDeviceID == "") || config.PhotoFramePushToken == "" {
		return Config{}, errors.New("PHOTOFRAME_PUSH_TOKEN and either PHOTOFRAME_PUSH_URL or PHOTOFRAME_DEVICE_ID are required")
	}
	if config.PhotoFrameTransport != "http" && config.PhotoFrameTransport != "ble" {
		return Config{}, errors.New("PHOTOFRAME_PUSH_TRANSPORT must be http or ble")
	}
	if config.PhotoFrameTransport == "ble" && config.PhotoFrameDeviceID == "" {
		return Config{}, errors.New("PHOTOFRAME_DEVICE_ID or a saved binding is required for BLE image transport")
	}
	if config.PhotoFramePushURL != "" {
		if err := validatePhotoFramePushURL(config.PhotoFramePushURL); err != nil {
			return Config{}, err
		}
	}
	if err := validatePhotoFramePushToken(config.PhotoFramePushToken); err != nil {
		return Config{}, err
	}
	for _, secret := range []struct {
		name  string
		value string
	}{
		{name: "CLIPROXY_MANAGEMENT_KEY", value: config.ManagementKey},
		{name: "CPAMP_ADMIN_KEY", value: config.CPAMPAdminKey},
	} {
		if secret.value != "" && config.PhotoFramePushToken == secret.value {
			return Config{}, fmt.Errorf("PHOTOFRAME_PUSH_TOKEN must be different from %s", secret.name)
		}
	}
	if config.CPAMPAdminKey != "" && config.CPAMPBaseURL == "" {
		config.CPAMPBaseURL = "http://127.0.0.1:18317"
	}
	if config.CPAMPBaseURL != "" && config.CPAMPAdminKey == "" && !googleAuth {
		return Config{}, errors.New("CPAMP_ADMIN_KEY is required when CPAMP_BASE_URL is set")
	}
	return config, nil
}

func validatePhotoFramePushURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || !parsed.IsAbs() || parsed.Hostname() == "" {
		return errors.New("PHOTOFRAME_PUSH_URL must be an absolute HTTP(S) URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("PHOTOFRAME_PUSH_URL must use http or https")
	}
	if parsed.User != nil {
		return errors.New("PHOTOFRAME_PUSH_URL must not contain credentials")
	}
	if parsed.EscapedPath() != "/api/push" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(rawURL, "#") {
		return errors.New("PHOTOFRAME_PUSH_URL must be a complete /api/push URL without query or fragment")
	}
	return nil
}

func validatePhotoFramePushToken(token string) error {
	if strings.HasPrefix(strings.ToLower(token), "replace-with-") {
		return errors.New("PHOTOFRAME_PUSH_TOKEN placeholder must be replaced")
	}
	if len(token) < 32 || len(token) > 128 {
		return errors.New("PHOTOFRAME_PUSH_TOKEN must be 32..128 printable ASCII bytes")
	}
	for index := 0; index < len(token); index++ {
		if token[index] < 0x21 || token[index] > 0x7e {
			return errors.New("PHOTOFRAME_PUSH_TOKEN must use printable ASCII without whitespace")
		}
	}
	return nil
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
