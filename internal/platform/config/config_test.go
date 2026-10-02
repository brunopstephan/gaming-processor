//go:build !integration && !e2e

package config

import (
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
