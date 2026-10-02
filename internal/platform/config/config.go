// Package config loads and validates the service configuration from the
// environment. Invalid configuration stops the process before it starts.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

// Config is the whole service configuration. Later plans add sections.
type Config struct {
	Database Database
	Wagering Wagering
	HTTP     HTTP
	Auth     Auth
	Log      Log
}

// Wagering configures how PENDING_REFERENCE operations wait for their reference.
type Wagering struct {
	ReferenceRetryBaseDelay   time.Duration
	ReferenceRetryMaxDelay    time.Duration
	ReferenceRetryMaxAttempts int
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
	wagering := Wagering{
		ReferenceRetryBaseDelay:   positiveDuration(getenv, "REFERENCE_RETRY_BASE_DELAY", 2*time.Second, &errs),
		ReferenceRetryMaxDelay:    positiveDuration(getenv, "REFERENCE_RETRY_MAX_DELAY", 5*time.Minute, &errs),
		ReferenceRetryMaxAttempts: positiveInt(getenv, "REFERENCE_RETRY_MAX_ATTEMPTS", 10, &errs),
	}
	if wagering.ReferenceRetryMaxDelay != 0 && wagering.ReferenceRetryMaxDelay < wagering.ReferenceRetryBaseDelay {
		errs = append(errs, fmt.Errorf("REFERENCE_RETRY_MAX_DELAY (%s) must be >= REFERENCE_RETRY_BASE_DELAY (%s)",
			wagering.ReferenceRetryMaxDelay, wagering.ReferenceRetryBaseDelay))
	}
	httpCfg := HTTP{
		Addr:            envOr(getenv, "HTTP_ADDR", ":8080"),
		ShutdownTimeout: positiveDuration(getenv, "HTTP_SHUTDOWN_TIMEOUT", 15*time.Second, &errs),
	}
	authCfg := Auth{
		IssuerURL: getenv("OIDC_ISSUER_URL"),
		JWKSURL:   getenv("OIDC_JWKS_URL"),
		Audience:  envOr(getenv, "OIDC_AUDIENCE", "wallet-api"),
	}
	if authCfg.JWKSURL == "" && authCfg.IssuerURL != "" {
		authCfg.JWKSURL = strings.TrimRight(authCfg.IssuerURL, "/") + "/protocol/openid-connect/certs"
	}
	logCfg := Log{Level: logLevel(getenv, &errs)}
	if len(errs) > 0 {
		return Config{}, fmt.Errorf("config: %w", errors.Join(errs...))
	}
	return Config{Database: db, Wagering: wagering, HTTP: httpCfg, Auth: authCfg, Log: logCfg}, nil
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

// HTTP configures the API server.
type HTTP struct {
	Addr            string
	ShutdownTimeout time.Duration
}

// Auth configures OIDC access-token validation. IssuerURL is required by the
// auth module (the HTTP API cannot start without it); JWKSURL may point to an
// internal host while IssuerURL is the public issuer in the tokens.
type Auth struct {
	IssuerURL string
	JWKSURL   string
	Audience  string
}

// Log configures the structured logger.
type Log struct {
	Level slog.Level
}

func envOr(getenv func(string) string, key, fallback string) string {
	if v := getenv(key); v != "" {
		return v
	}
	return fallback
}

func logLevel(getenv func(string) string, errs *[]error) slog.Level {
	switch strings.ToLower(getenv("LOG_LEVEL")) {
	case "", "info":
		return slog.LevelInfo
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		*errs = append(*errs, fmt.Errorf("LOG_LEVEL must be debug, info, warn or error, got %q", getenv("LOG_LEVEL")))
		return slog.LevelInfo
	}
}
