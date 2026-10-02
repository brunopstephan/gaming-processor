package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
)

// transientCodes are SQLSTATEs worth retrying: serialization failure,
// deadlock, lock_timeout, statement_timeout/cancel, server shutdown and
// connection pressure. Class 08 (connection exception) is matched by prefix.
var transientCodes = map[string]bool{
	"40001": true, "40P01": true, "55P03": true, "57014": true,
	"57P01": true, "57P02": true, "57P03": true, "53300": true,
}

// mapError classifies database errors into app sentinels while keeping the
// original error in the chain. Unknown errors are returned unchanged and are
// treated as permanent by callers. Context cancellation and deadline expiry
// are transient: they say nothing about the request itself, so they must never
// lead to a permanent FAILED outcome.
func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, app.ErrNotFound) || errors.Is(err, app.ErrConflict) ||
		errors.Is(err, app.ErrVersionConflict) || errors.Is(err, app.ErrTransient) {
		return err // already classified (e.g. by a repository)
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("%w: %w", app.ErrNotFound, err)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case pgErr.Code == "23505":
			return fmt.Errorf("%w: %s: %w", app.ErrConflict, pgErr.ConstraintName, err)
		case pgErr.Code == "55P03":
			return fmt.Errorf("%w: %w", app.ErrLockTimeout, err)
		case transientCodes[pgErr.Code] || strings.HasPrefix(pgErr.Code, "08"):
			return fmt.Errorf("%w: %w", app.ErrTransient, err)
		default:
			return err
		}
	}
	var connErr *pgconn.ConnectError
	var netErr net.Error
	if errors.As(err, &connErr) || errors.As(err, &netErr) ||
		errors.Is(err, driver.ErrBadConn) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, pgconn.ErrConnClosed) || errors.Is(err, sql.ErrConnDone) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, context.Canceled) {
		return fmt.Errorf("%w: %w", app.ErrTransient, err)
	}
	return err
}
