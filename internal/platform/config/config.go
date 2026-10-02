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
	Database  Database
	Wagering  Wagering
	HTTP      HTTP
	Auth      Auth
	Log       Log
	SQS       SQS
	Toggles   Toggles
	Outbox    Outbox
	RefWorker RefWorker
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
	sqsCfg := SQS{
		Endpoint:         getenv("SQS_ENDPOINT"),
		Region:           envOr(getenv, "AWS_REGION", "us-east-1"),
		ConsumerProfile:  getenv("SQS_CONSUMER_PROFILE"),
		PublisherProfile: getenv("SQS_PUBLISHER_PROFILE"),
		InputQueue:       envOr(getenv, "SQS_INPUT_QUEUE", "wager-transactions.fifo"),
		InputDLQ:         envOr(getenv, "SQS_INPUT_DLQ", "wager-transactions-dlq.fifo"),
		EventsQueue:      envOr(getenv, "SQS_EVENTS_QUEUE", "wallet-events.fifo"),
		Workers:          positiveInt(getenv, "SQS_CONSUMER_WORKERS", 4, &errs),
		WaitTime:         positiveDuration(getenv, "SQS_WAIT_TIME", 20*time.Second, &errs),
		MaxMessages:      positiveInt(getenv, "SQS_MAX_MESSAGES", 10, &errs),
		RetryBaseDelay:   positiveDuration(getenv, "SQS_RETRY_BASE_DELAY", 2*time.Second, &errs),
		RetryMaxDelay:    positiveDuration(getenv, "SQS_RETRY_MAX_DELAY", 5*time.Minute, &errs),
		ShutdownTimeout:  positiveDuration(getenv, "SQS_SHUTDOWN_TIMEOUT", 20*time.Second, &errs),
		SenderProviders:  senderProviders(getenv, &errs),
	}
	if sqsCfg.WaitTime < time.Second || sqsCfg.WaitTime > 20*time.Second {
		errs = append(errs, fmt.Errorf("SQS_WAIT_TIME must be between 1s and 20s, got %s", sqsCfg.WaitTime))
	}
	if sqsCfg.MaxMessages > 10 {
		errs = append(errs, fmt.Errorf("SQS_MAX_MESSAGES must be between 1 and 10, got %d", sqsCfg.MaxMessages))
	}
	if sqsCfg.RetryBaseDelay < time.Second {
		errs = append(errs, fmt.Errorf("SQS_RETRY_BASE_DELAY must be at least 1s (visibility timeouts are whole seconds), got %s", sqsCfg.RetryBaseDelay))
	}
	if sqsCfg.RetryMaxDelay < sqsCfg.RetryBaseDelay || sqsCfg.RetryMaxDelay > 12*time.Hour {
		errs = append(errs, fmt.Errorf("SQS_RETRY_MAX_DELAY must be >= SQS_RETRY_BASE_DELAY and <= 12h, got %s", sqsCfg.RetryMaxDelay))
	}
	toggles := Toggles{
		HTTP:      boolEnv(getenv, "HTTP_ENABLED", &errs),
		Consumer:  boolEnv(getenv, "CONSUMER_ENABLED", &errs),
		Outbox:    boolEnv(getenv, "OUTBOX_ENABLED", &errs),
		RefWorker: boolEnv(getenv, "REFWORKER_ENABLED", &errs),
	}
	outbox := Outbox{
		PollInterval:    positiveDuration(getenv, "OUTBOX_POLL_INTERVAL", 500*time.Millisecond, &errs),
		BatchSize:       positiveInt(getenv, "OUTBOX_BATCH_SIZE", 50, &errs),
		Lease:           positiveDuration(getenv, "OUTBOX_LEASE", 30*time.Second, &errs),
		RetryBaseDelay:  positiveDuration(getenv, "OUTBOX_RETRY_BASE_DELAY", time.Second, &errs),
		RetryMaxDelay:   positiveDuration(getenv, "OUTBOX_RETRY_MAX_DELAY", 5*time.Minute, &errs),
		ShutdownTimeout: positiveDuration(getenv, "OUTBOX_SHUTDOWN_TIMEOUT", 10*time.Second, &errs),
	}
	if outbox.RetryMaxDelay < outbox.RetryBaseDelay {
		errs = append(errs, fmt.Errorf("OUTBOX_RETRY_MAX_DELAY (%s) must be >= OUTBOX_RETRY_BASE_DELAY (%s)", outbox.RetryMaxDelay, outbox.RetryBaseDelay))
	}
	refWorker := RefWorker{
		PollInterval:    positiveDuration(getenv, "REFWORKER_POLL_INTERVAL", time.Second, &errs),
		ShutdownTimeout: positiveDuration(getenv, "REFWORKER_SHUTDOWN_TIMEOUT", 10*time.Second, &errs),
	}
	if len(errs) > 0 {
		return Config{}, fmt.Errorf("config: %w", errors.Join(errs...))
	}
	return Config{Database: db, Wagering: wagering, HTTP: httpCfg, Auth: authCfg, Log: logCfg,
		SQS: sqsCfg, Toggles: toggles, Outbox: outbox, RefWorker: refWorker}, nil
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

// SQS configures the SQS clients and the input consumer. Profiles name
// entries of the AWS shared credentials file (AWS_SHARED_CREDENTIALS_FILE);
// empty means the SDK's default credential chain.
type SQS struct {
	Endpoint         string
	Region           string
	ConsumerProfile  string
	PublisherProfile string
	InputQueue       string
	InputDLQ         string
	EventsQueue      string
	Workers          int
	WaitTime         time.Duration
	MaxMessages      int
	RetryBaseDelay   time.Duration
	RetryMaxDelay    time.Duration
	ShutdownTimeout  time.Duration
	// SenderProviders maps the SQS SenderId (the sending account) to the providerId it represents.
	SenderProviders map[string]string
}

// Toggles turn process components on or off (tests and dedicated workers).
type Toggles struct {
	HTTP      bool
	Consumer  bool
	Outbox    bool
	RefWorker bool
}

// Outbox configures the outbox relay.
type Outbox struct {
	PollInterval    time.Duration
	BatchSize       int
	Lease           time.Duration
	RetryBaseDelay  time.Duration
	RetryMaxDelay   time.Duration
	ShutdownTimeout time.Duration
}

// RefWorker configures the PENDING_REFERENCE worker.
type RefWorker struct {
	PollInterval    time.Duration
	ShutdownTimeout time.Duration
}

func boolEnv(getenv func(string) string, key string, errs *[]error) bool {
	switch strings.ToLower(strings.TrimSpace(getenv(key))) {
	case "", "true", "1":
		return true
	case "false", "0":
		return false
	default:
		*errs = append(*errs, fmt.Errorf("%s must be true, false, 1 or 0, got %q", key, getenv(key)))
		return true
	}
}

// maxProviderIDBytes bounds a mapped providerId: the inbox consumer name is
// "wager-transactions/<providerId>" and consumer_name is varchar(100).
const maxProviderIDBytes = 64

// senderProviders parses "account=provider,account=provider".
func senderProviders(getenv func(string) string, errs *[]error) map[string]string {
	raw := envOr(getenv, "SQS_SENDER_PROVIDER_MAP", "111111111111=provider-a,222222222222=provider-b")
	out := map[string]string{}
	for _, pair := range strings.Split(raw, ",") {
		sender, provider, ok := strings.Cut(pair, "=")
		sender, provider = strings.TrimSpace(sender), strings.TrimSpace(provider)
		if !ok || sender == "" || provider == "" {
			*errs = append(*errs, fmt.Errorf("SQS_SENDER_PROVIDER_MAP entry %q must be sender=provider", pair))
			continue
		}
		if len(provider) > maxProviderIDBytes {
			*errs = append(*errs, fmt.Errorf("SQS_SENDER_PROVIDER_MAP provider %q is longer than %d bytes", provider, maxProviderIDBytes))
			continue
		}
		out[sender] = provider
	}
	return out
}
