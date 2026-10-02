//go:build !integration && !e2e

package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
)

func TestMapError(t *testing.T) {
	pg := func(code string) error {
		return fmt.Errorf("wrapped: %w", &pgconn.PgError{Code: code, ConstraintName: "some_constraint"})
	}
	tests := []struct {
		name string
		in   error
		want error
	}{
		{"not found", gorm.ErrRecordNotFound, app.ErrNotFound},
		{"unique violation", pg("23505"), app.ErrConflict},
		{"lock timeout", pg("55P03"), app.ErrTransient},
		{"serialization", pg("40001"), app.ErrTransient},
		{"deadlock", pg("40P01"), app.ErrTransient},
		{"statement timeout", pg("57014"), app.ErrTransient},
		{"admin shutdown", pg("57P01"), app.ErrTransient},
		{"too many connections", pg("53300"), app.ErrTransient},
		{"connection exception class", pg("08006"), app.ErrTransient},
		{"bad conn", driver.ErrBadConn, app.ErrTransient},
		{"deadline", context.DeadlineExceeded, app.ErrTransient},
		{"canceled", context.Canceled, app.ErrTransient},
		{"wrapped canceled", fmt.Errorf("w: %w", context.Canceled), app.ErrTransient},
		{"version conflict", app.ErrVersionConflict, app.ErrTransient},
		{"eof", io.EOF, app.ErrTransient},
		{"unexpected eof", io.ErrUnexpectedEOF, app.ErrTransient},
		{"pgconn closed", pgconn.ErrConnClosed, app.ErrTransient},
		{"sql conn done", sql.ErrConnDone, app.ErrTransient},
		{"wrapped eof", fmt.Errorf("w: %w", io.EOF), app.ErrTransient},
		{"wrapped unexpected eof", fmt.Errorf("w: %w", io.ErrUnexpectedEOF), app.ErrTransient},
		{"wrapped pgconn closed", fmt.Errorf("w: %w", pgconn.ErrConnClosed), app.ErrTransient},
		{"wrapped conn done", fmt.Errorf("w: %w", sql.ErrConnDone), app.ErrTransient},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapError(tt.in)
			if !errors.Is(got, tt.want) {
				t.Fatalf("mapError(%v) = %v, want errors.Is %v", tt.in, got, tt.want)
			}
			if !errors.Is(got, tt.in) {
				t.Fatalf("mapError must keep the original error in the chain: %v", got)
			}
		})
	}

	for _, permanent := range []error{pg("23514"), pg("42501"), pg("P0001"), errors.New("boom")} {
		got := mapError(permanent)
		if errors.Is(got, app.ErrTransient) || errors.Is(got, app.ErrConflict) || errors.Is(got, app.ErrNotFound) {
			t.Errorf("mapError(%v) = %v, want unclassified", permanent, got)
		}
	}
	if mapError(nil) != nil {
		t.Fatal("mapError(nil) must be nil")
	}
	if got := mapError(pg("23505")); !strings.Contains(got.Error(), "some_constraint") {
		t.Fatalf("conflict must name the constraint: %v", got)
	}

	once := mapError(mapError(pg("23505")))
	if n := strings.Count(once.Error(), "some_constraint"); n != 1 {
		t.Fatalf("constraint named %d times, want once: %v", n, once)
	}
}

func TestMapErrorLockTimeout(t *testing.T) {
	got := mapError(fmt.Errorf("query: %w", &pgconn.PgError{Code: "55P03"}))
	if !errors.Is(got, app.ErrLockTimeout) || !errors.Is(got, app.ErrTransient) {
		t.Fatalf("mapError(55P03) = %v, want ErrLockTimeout (and ErrTransient)", got)
	}
	if got := mapError(fmt.Errorf("query: %w", &pgconn.PgError{Code: "40P01"})); errors.Is(got, app.ErrLockTimeout) {
		t.Fatalf("deadlock must not be reported as lock timeout: %v", got)
	}
}
