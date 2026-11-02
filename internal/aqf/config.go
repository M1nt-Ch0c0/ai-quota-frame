package aqf

import (
	"errors"
	"os"
	"time"
)

// Keepalive stays well inside the host's 60-second idle session timeout.
const Keepalive = 20 * time.Second

const defaultSessionIdle = 10 * time.Minute

// SessionIdle reads AQF_SESSION_IDLE: how long the daemon keeps an unused
// session open. "0" keeps it until a failure or an explicit disconnect.
func SessionIdle() (time.Duration, error) {
	value := os.Getenv("AQF_SESSION_IDLE")
	if value == "" {
		return defaultSessionIdle, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil || d < 0 {
		return 0, errors.New("AQF_SESSION_IDLE must be a non-negative duration such as 10m or 0")
	}
	return d, nil
}
