//go:build !integration && !e2e

package config

import (
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"
)

func env(values map[string]string) func(string) string {
	return func(k string) string { return values[k] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(env(map[string]string{"DATABASE_URL": "postgres://x"}))
	if err != nil {
		t.Fatal(err)
	}
	want := Database{URL: "postgres://x", MaxOpenConns: 20, LockTimeout: 5 * time.Second, StatementTimeout: 10 * time.Second}
	if cfg.Database != want {
		t.Fatalf("Database = %+v, want %+v", cfg.Database, want)
	}
}

func TestLoadOverrides(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"DATABASE_URL": "postgres://y", "DB_MAX_OPEN_CONNS": "7",
		"DB_LOCK_TIMEOUT": "250ms", "DB_STATEMENT_TIMEOUT": "3s",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Database.MaxOpenConns != 7 || cfg.Database.LockTimeout != 250*time.Millisecond ||
		cfg.Database.StatementTimeout != 3*time.Second {
		t.Fatalf("Database = %+v", cfg.Database)
	}
}

func TestLoadInvalid(t *testing.T) {
	_, err := Load(env(map[string]string{
		"DB_MAX_OPEN_CONNS": "zero", "DB_LOCK_TIMEOUT": "-1s", "DB_STATEMENT_TIMEOUT": "soon",
	}))
	if err == nil {
		t.Fatal("Load succeeded, want error")
	}
	for _, key := range []string{"DATABASE_URL", "DB_MAX_OPEN_CONNS", "DB_LOCK_TIMEOUT", "DB_STATEMENT_TIMEOUT"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error %q does not mention %s", err, key)
		}
	}
}

func TestLoadDurationBounds(t *testing.T) {
	for _, v := range []string{"500us", "25h"} {
		for _, key := range []string{"DB_LOCK_TIMEOUT", "DB_STATEMENT_TIMEOUT"} {
			_, err := Load(env(map[string]string{"DATABASE_URL": "postgres://x", key: v}))
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Errorf("%s=%s: err = %v, want error naming key", key, v, err)
			}
		}
	}
	if _, err := Load(env(map[string]string{"DATABASE_URL": "postgres://x", "DB_LOCK_TIMEOUT": "1ms", "DB_STATEMENT_TIMEOUT": "24h"})); err != nil {
		t.Fatalf("bounds must be inclusive: %v", err)
	}
}

func TestLoadWageringDefaultsAndOverrides(t *testing.T) {
	cfg, err := Load(env(map[string]string{"DATABASE_URL": "postgres://x"}))
	if err != nil {
		t.Fatal(err)
	}
	want := Wagering{ReferenceRetryBaseDelay: 2 * time.Second, ReferenceRetryMaxDelay: 5 * time.Minute, ReferenceRetryMaxAttempts: 10}
	if cfg.Wagering != want {
		t.Fatalf("Wagering = %+v, want %+v", cfg.Wagering, want)
	}
	cfg, err = Load(env(map[string]string{
		"DATABASE_URL": "postgres://x", "REFERENCE_RETRY_BASE_DELAY": "100ms",
		"REFERENCE_RETRY_MAX_DELAY": "1s", "REFERENCE_RETRY_MAX_ATTEMPTS": "3",
	}))
	if err != nil || cfg.Wagering.ReferenceRetryBaseDelay != 100*time.Millisecond || cfg.Wagering.ReferenceRetryMaxAttempts != 3 {
		t.Fatalf("overrides = %+v, %v", cfg.Wagering, err)
	}
}

func TestLoadWageringInvalid(t *testing.T) {
	_, err := Load(env(map[string]string{
		"DATABASE_URL": "postgres://x", "REFERENCE_RETRY_BASE_DELAY": "10s", "REFERENCE_RETRY_MAX_DELAY": "1s",
		"REFERENCE_RETRY_MAX_ATTEMPTS": "0",
	}))
	if err == nil || !strings.Contains(err.Error(), "REFERENCE_RETRY_MAX_DELAY") || !strings.Contains(err.Error(), "REFERENCE_RETRY_MAX_ATTEMPTS") {
		t.Fatalf("error = %v, want both keys reported", err)
	}
}

func TestLoadHTTPAuthLogDefaults(t *testing.T) {
	cfg, err := Load(env(map[string]string{"DATABASE_URL": "postgres://x"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP.Addr != ":8080" || cfg.HTTP.ShutdownTimeout != 15*time.Second {
		t.Fatalf("HTTP = %+v", cfg.HTTP)
	}
	if cfg.Auth.IssuerURL != "" || cfg.Auth.Audience != "wallet-api" || cfg.Log.Level != slog.LevelInfo {
		t.Fatalf("Auth %+v Log %+v", cfg.Auth, cfg.Log)
	}
}

func TestLoadAuthDerivesJWKS(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"DATABASE_URL": "postgres://x", "OIDC_ISSUER_URL": "http://kc/realms/wallet",
		"HTTP_ADDR": "127.0.0.1:0", "LOG_LEVEL": "DEBUG",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auth.JWKSURL != "http://kc/realms/wallet/protocol/openid-connect/certs" ||
		cfg.HTTP.Addr != "127.0.0.1:0" || cfg.Log.Level != slog.LevelDebug {
		t.Fatalf("cfg = %+v", cfg)
	}
	cfg, _ = Load(env(map[string]string{
		"DATABASE_URL": "postgres://x", "OIDC_ISSUER_URL": "http://a", "OIDC_JWKS_URL": "http://internal/certs",
	}))
	if cfg.Auth.JWKSURL != "http://internal/certs" {
		t.Fatalf("explicit JWKS ignored: %s", cfg.Auth.JWKSURL)
	}
}

func TestLoadInvalidLogLevelAndTimeout(t *testing.T) {
	_, err := Load(env(map[string]string{"DATABASE_URL": "postgres://x", "LOG_LEVEL": "loud", "HTTP_SHUTDOWN_TIMEOUT": "0s"}))
	if err == nil || !strings.Contains(err.Error(), "LOG_LEVEL") || !strings.Contains(err.Error(), "HTTP_SHUTDOWN_TIMEOUT") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadMessagingDefaults(t *testing.T) {
	cfg, err := Load(env(map[string]string{"DATABASE_URL": "postgres://x"}))
	if err != nil {
		t.Fatal(err)
	}
	wantSQS := SQS{
		Region: "us-east-1", InputQueue: "wager-transactions.fifo", InputDLQ: "wager-transactions-dlq.fifo",
		EventsQueue: "wallet-events.fifo", Workers: 4, WaitTime: 20 * time.Second, MaxMessages: 10,
		RetryBaseDelay: 2 * time.Second, RetryMaxDelay: 5 * time.Minute, ShutdownTimeout: 20 * time.Second,
		SenderProviders: map[string]string{"111111111111": "provider-a", "222222222222": "provider-b"},
	}
	if !reflect.DeepEqual(cfg.SQS, wantSQS) {
		t.Fatalf("SQS = %+v\nwant %+v", cfg.SQS, wantSQS)
	}
	if cfg.Toggles != (Toggles{HTTP: true, Consumer: true, Outbox: true, RefWorker: true}) {
		t.Fatalf("Toggles = %+v", cfg.Toggles)
	}
	wantOutbox := Outbox{PollInterval: 500 * time.Millisecond, BatchSize: 50, Lease: 30 * time.Second,
		RetryBaseDelay: time.Second, RetryMaxDelay: 5 * time.Minute, ShutdownTimeout: 10 * time.Second}
	if cfg.Outbox != wantOutbox || cfg.RefWorker != (RefWorker{PollInterval: time.Second, ShutdownTimeout: 10 * time.Second}) {
		t.Fatalf("Outbox %+v RefWorker %+v", cfg.Outbox, cfg.RefWorker)
	}
}

func TestLoadMessagingOverrides(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"DATABASE_URL": "postgres://x", "SQS_ENDPOINT": "http://localhost:4566", "SQS_CONSUMER_PROFILE": "wallet-consumer",
		"SQS_SENDER_PROVIDER_MAP": " 333333333333 = provider-c ", "HTTP_ENABLED": "false", "CONSUMER_ENABLED": "0",
		"SQS_WAIT_TIME": "1s", "SQS_MAX_MESSAGES": "3",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SQS.Endpoint != "http://localhost:4566" || cfg.SQS.ConsumerProfile != "wallet-consumer" ||
		!reflect.DeepEqual(cfg.SQS.SenderProviders, map[string]string{"333333333333": "provider-c"}) ||
		cfg.Toggles.HTTP || cfg.Toggles.Consumer || !cfg.Toggles.Outbox || cfg.SQS.WaitTime != time.Second || cfg.SQS.MaxMessages != 3 {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestLoadMessagingInvalid(t *testing.T) {
	_, err := Load(env(map[string]string{
		"DATABASE_URL": "postgres://x", "SQS_SENDER_PROVIDER_MAP": "broken", "OUTBOX_ENABLED": "maybe",
		"SQS_WAIT_TIME": "30s", "SQS_MAX_MESSAGES": "11", "SQS_RETRY_MAX_DELAY": "1s",
		"OUTBOX_RETRY_MAX_DELAY": "1ms",
	}))
	for _, key := range []string{"SQS_SENDER_PROVIDER_MAP", "OUTBOX_ENABLED", "SQS_WAIT_TIME", "SQS_MAX_MESSAGES", "SQS_RETRY_MAX_DELAY", "OUTBOX_RETRY_MAX_DELAY"} {
		if err == nil || !strings.Contains(err.Error(), key) {
			t.Errorf("error %v does not mention %s", err, key)
		}
	}
}
