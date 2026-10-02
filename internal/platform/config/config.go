// Package config loads and validates the service configuration from the
// environment. Invalid configuration stops the process before it starts.
package config

import (
	"errors"
	"fmt"
	"strconv"
	"time"
)

// Config is the whole service configuration. Later plans add sections.
type Config struct {
	Database Database
}

// Database configures the PostgreSQL connection pool and per-transaction limits.
type Database struct {
	URL              string
	MaxOpenConns     int
	LockTimeout      time.Duration
	StatementTimeout time.Duration
}

// Load reads the configuration through getenv (os.Getenv in production).
// Every invalid variable is reported at once.
func Load(getenv func(string) string) (Config, error) {
	var errs []error
	db := Database{
		URL:              getenv("DATABASE_URL"),
		MaxOpenConns:     positiveInt(getenv, "DB_MAX_OPEN_CONNS", 20, &errs),
		LockTimeout:      positiveDuration(getenv, "DB_LOCK_TIMEOUT", 5*time.Second, &errs),
		StatementTimeout: positiveDuration(getenv, "DB_STATEMENT_TIMEOUT", 10*time.Second, &errs),
	}
	if db.URL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required"))
	}
	if len(errs) > 0 {
		return Config{}, fmt.Errorf("config: %w", errors.Join(errs...))
	}
	return Config{Database: db}, nil
}

func positiveInt(getenv func(string) string, key string, fallback int, errs *[]error) int {
	raw := getenv(key)
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		*errs = append(*errs, fmt.Errorf("%s must be a positive integer, got %q", key, raw))
		return 0
	}
	return v
}

func positiveDuration(getenv func(string) string, key string, fallback time.Duration, errs *[]error) time.Duration {
	raw := getenv(key)
	if raw == "" {
		return fallback
	}
	v, err := time.ParseDuration(raw)
	if err != nil || v < time.Millisecond || v > 24*time.Hour {
		*errs = append(*errs, fmt.Errorf("%s must be a duration between 1ms and 24h, got %q", key, raw))
		return 0
	}
	return v
}
