// Package pgtest connects integration tests to the postgres_test container
// started with `docker compose --profile test up -d --wait postgres_test`
// and migrated with `docker compose --profile test run --rm migrate_test`.
package pgtest

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	defaultAppURL   = "postgres://wallet_app:wallet_app@localhost:5433/wallet?sslmode=disable"
	defaultOwnerURL = "postgres://wallet_owner:wallet_owner@localhost:5433/wallet?sslmode=disable"

	// MigrationsPath is the migrations directory relative to internal/adapters/postgres.
	MigrationsPath = "../../../migrations"
)

// AppURL is the least-privilege application connection string.
func AppURL() string { return envOr("TEST_DATABASE_URL", defaultAppURL) }

// OwnerURL is the migration-owner connection string.
func OwnerURL() string { return envOr("TEST_DATABASE_OWNER_URL", defaultOwnerURL) }

// WithDatabase returns rawURL pointing at database name.
func WithDatabase(rawURL, name string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		panic(err) // test configuration bug, never a runtime path
	}
	u.Path = "/" + name
	return u.String()
}

// AppDB opens a connection as wallet_app.
func AppDB(t testing.TB) *gorm.DB { return Open(t, AppURL()) }

// OwnerDB opens a connection as wallet_owner.
func OwnerDB(t testing.TB) *gorm.DB { return Open(t, OwnerURL()) }

// Open connects to url, pings it and closes it at test cleanup. It fails the
// test with setup instructions when the database is unreachable.
func Open(t testing.TB, rawURL string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(postgres.Open(rawURL), &gorm.Config{
		SkipDefaultTransaction: true,
		Logger:                 logger.Discard,
	})
	if err != nil {
		t.Fatalf("postgres de teste indisponível (%v). Suba a infra de teste: "+
			"`docker compose --profile test up -d --wait postgres_test && "+
			"docker compose --profile test run --rm migrate_test`", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

// FreshDatabase creates a new database migrated to the latest version,
// dropped at cleanup, and returns its wallet_app URL. Tests whose code claims
// rows globally (outbox relay, reference worker) use it so leftovers of other
// tests are never claimed.
func FreshDatabase(t testing.TB) string {
	t.Helper()
	owner := OwnerDB(t)
	name := fmt.Sprintf("wallet_t_%d_%s", time.Now().UnixNano(), strings.ReplaceAll(uuid.NewString(), "-", "")[:8])
	if err := owner.Exec("CREATE DATABASE " + name).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := owner.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)").Error; err != nil {
			t.Logf("drop database %s: %v", name, err)
		}
	})
	_, file, _, _ := runtime.Caller(0)
	migrations := filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations")
	ownerURL := strings.Replace(WithDatabase(OwnerURL(), name), "postgres://", "pgx5://", 1)
	m, err := migrate.New("file://"+migrations, ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := m.Up(); err != nil {
		t.Fatalf("migrate fresh database: %v", err)
	}
	return WithDatabase(AppURL(), name)
}

// RequirePgError fails unless err carries a postgres error with code and,
// when constraint is not empty, that constraint name.
func RequirePgError(t testing.TB, err error, code, constraint string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("error = %v, want postgres error %s", err, code)
	}
	if pgErr.Code != code || (constraint != "" && pgErr.ConstraintName != constraint) {
		t.Fatalf("postgres error %s constraint %q (%s), want %s constraint %q",
			pgErr.Code, pgErr.ConstraintName, pgErr.Message, code, constraint)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
