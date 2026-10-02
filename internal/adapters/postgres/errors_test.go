//go:build !integration && !e2e

package postgres

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
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

	for _, permanent := range []error{pg("23514"), pg("42501"), pg("P0001"), errors.New("boom"), context.Canceled} {
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
}
