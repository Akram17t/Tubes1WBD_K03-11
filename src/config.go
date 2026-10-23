package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// the data structure for config (env var for now)
type Config struct {
	SessionIdleTimeout      time.Duration
	SessionAbsoluteTimeout  time.Duration
	SessionRotationInterval time.Duration
	SessionGracePeriod      time.Duration
}

// LoadConfig reads session settings from the environment. Unset variables use defaults
func LoadConfig() (Config, error) {
	var config Config
	var err error

	if config.SessionIdleTimeout, err = durationFromEnv("SESSION_IDLE_TIMEOUT", 30*time.Minute); err != nil {
		return Config{}, err
	}
	if config.SessionAbsoluteTimeout, err = durationFromEnv("SESSION_ABSOLUTE_TIMEOUT", 24*time.Hour); err != nil {
		return Config{}, err
	}
	if config.SessionRotationInterval, err = durationFromEnv("SESSION_ROTATION_INTERVAL", 15*time.Minute); err != nil {
		return Config{}, err
	}
	if config.SessionGracePeriod, err = durationFromEnv("SESSION_GRACE_PERIOD", 30*time.Second); err != nil {
		return Config{}, err
	}

	if config.SessionIdleTimeout > config.SessionAbsoluteTimeout {
		return Config{}, fmt.Errorf("SESSION_IDLE_TIMEOUT must not exceed SESSION_ABSOLUTE_TIMEOUT")
	}
	if config.SessionRotationInterval > config.SessionAbsoluteTimeout {
		return Config{}, fmt.Errorf("SESSION_ROTATION_INTERVAL must not exceed SESSION_ABSOLUTE_TIMEOUT")
	}
	if config.SessionGracePeriod > config.SessionRotationInterval {
		return Config{}, fmt.Errorf("SESSION_GRACE_PERIOD must not exceed SESSION_ROTATION_INTERVAL")
	}

	return config, nil
}

func durationFromEnv(name string, defaultValue time.Duration) (time.Duration, error) {
	raw, ok := os.LookupEnv(name)
	if !ok {
		return defaultValue, nil
	}

	value, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration such as 30m or 24h: %w", name, err)
	}
	if value <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", name)
	}
	return value, nil
}
