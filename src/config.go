package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

type Config struct {
	AppAddr                 string
	AppEnv                  string
	PlatformDatabaseURL     string
	SessionIdleTimeout      time.Duration
	SessionAbsoluteTimeout  time.Duration
	SessionRotationInterval time.Duration
	SessionGracePeriod      time.Duration
	AdminUsername           string
	AdminPassword           string
}

// LoadConfig reads .env defaults, then validates application settings.
func LoadConfig() (Config, error) {
	if err := loadDotEnv(".env"); err != nil {
		return Config{}, err
	}

	var config Config
	var err error

	config.AppAddr = envOrDefault("APP_ADDR", ":8080")
	if config.AppAddr == "" {
		return Config{}, fmt.Errorf("APP_ADDR must not be empty")
	}
	config.AppEnv = envOrDefault("APP_ENV", "development")
	if config.AppEnv != "development" && config.AppEnv != "production" && config.AppEnv != "test" {
		return Config{}, fmt.Errorf("APP_ENV must be development, production, or test")
	}
	config.PlatformDatabaseURL = strings.TrimSpace(os.Getenv("PLATFORM_DATABASE_URL"))
	if config.PlatformDatabaseURL == "" {
		return Config{}, fmt.Errorf("PLATFORM_DATABASE_URL must not be empty")
	}

	if config.SessionIdleTimeout, err = durationFromEnv("SESSION_IDLE_TIMEOUT", 30*time.Minute); err != nil {
		return Config{}, err
	}
	if config.SessionAbsoluteTimeout, err = durationFromEnv("SESSION_ABSOLUTE_TIMEOUT", 8*time.Hour); err != nil {
		return Config{}, err
	}
	if config.SessionRotationInterval, err = durationFromEnv("SESSION_ROTATION_INTERVAL", 15*time.Minute); err != nil {
		return Config{}, err
	}
	if config.SessionGracePeriod, err = durationFromEnv("SESSION_GRACE_PERIOD", 30*time.Second); err != nil {
		return Config{}, err
	}

	// dipakai buat bikin Platform Admin pertama, password ga di-trim
	config.AdminUsername = envOrDefault("ADMIN_USERNAME", "")
	config.AdminPassword = os.Getenv("ADMIN_PASSWORD")

	if config.SessionIdleTimeout > config.SessionAbsoluteTimeout {
		return Config{}, fmt.Errorf("SESSION_IDLE_TIMEOUT must not exceed SESSION_ABSOLUTE_TIMEOUT")
	}
	if config.SessionRotationInterval >= config.SessionIdleTimeout {
		return Config{}, fmt.Errorf("SESSION_ROTATION_INTERVAL must be less than SESSION_IDLE_TIMEOUT")
	}
	if config.SessionGracePeriod >= config.SessionRotationInterval {
		return Config{}, fmt.Errorf("SESSION_GRACE_PERIOD must be less than SESSION_ROTATION_INTERVAL")
	}

	return config, nil
}

func envOrDefault(name, fallback string) string {
	if value, ok := os.LookupEnv(name); ok {
		return strings.TrimSpace(value)
	}
	return fallback
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

// reads simple KEY=VALUE lines without overriding the process environment.
func loadDotEnv(path string) error {
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); !exists {
			value = strings.TrimSpace(value)
			if len(value) >= 2 && value[0] == value[len(value)-1] && (value[0] == '"' || value[0] == '\'') {
				value = value[1 : len(value)-1]
			}
			if err := os.Setenv(key, value); err != nil {
				return err
			}
		}
	}
	return nil
}
