//go:build !integration && !e2e

package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/health"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/metrics"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/authtest"
)

func TestErrorResponseMapping(t *testing.T) {
	input := &wagering.InputError{Violations: []wagering.Violation{{Code: wagering.FailureInvalidMoney, Field: "money.amount", Reason: "bad"}}}
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"input", input, 400, "INVALID_MONEY"},
		{"transient first", fmt.Errorf("x: %w", app.ErrVersionConflict), 503, "TEMPORARILY_UNAVAILABLE"},
		{"transient beats conflict", errors.Join(app.ErrIdempotencyKeyConflict, app.ErrTransient), 503, "TEMPORARILY_UNAVAILABLE"},
		{"key conflict", app.ErrIdempotencyKeyConflict, 409, "IDEMPOTENCY_KEY_CONFLICT"},
		{"external conflict", app.ErrExternalTransactionConflict, 409, "EXTERNAL_TRANSACTION_CONFLICT"},
		{"wallet exists", app.ErrWalletAlreadyExists, 409, "WALLET_ALREADY_EXISTS"},
		{"not found", fmt.Errorf("x: %w", app.ErrNotFound), 404, "NOT_FOUND"},
		{"cursor", app.ErrInvalidCursor, 400, "INVALID_CURSOR"},
		{"limit", app.ErrInvalidLimit, 400, "INVALID_LIMIT"},
		{"unknown", errors.New("boom"), 500, "INTERNAL_ERROR"},
		{"inside tx", app.ErrInsideTransaction, 500, "INTERNAL_ERROR"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, body := errorResponse(tt.err)
			if status != tt.status || body.Error.FailureCode != tt.code {
				t.Fatalf("errorResponse = %d %s, want %d %s", status, body.Error.FailureCode, tt.status, tt.code)
			}
		})
	}
	_, body := errorResponse(input)
	if body.Error.Category != "CORRECTABLE" || len(body.Error.Details) != 1 || body.Error.Details[0].Field != "money.amount" {
		t.Fatalf("input body = %+v", body)
	}
	if _, body := errorResponse(errors.New("secret detail")); strings.Contains(body.Error.Message, "secret") {
		t.Fatal("500 must not leak internal error text")
	}
}

func TestWriteErrorDoesNotLeakUnknownError(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	e := gin.New()
	e.GET("/x", func(c *gin.Context) { writeError(c, errors.New("secret detail")) })
	rec := do(e, "GET", "/x", "", "")
	if rec.Code != 500 || strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("500 body leaked or wrong status: %d %s", rec.Code, rec.Body)
	}
}

func testRouter(t *testing.T, checks ...health.Check) (*gin.Engine, *health.Readiness) {
	t.Helper()
	r := health.NewReadiness(checks)
	return NewRouter(RouterDeps{
		Auth: authtest.Default(), Metrics: metrics.New(), Readiness: r, Log: slog.New(slog.DiscardHandler),
	}), r
}

func do(router http.Handler, method, path, token string, body string, header ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestHealthAndMetricsArePublic(t *testing.T) {
	ok := health.Check{Name: "postgres", Probe: func(context.Context) error { return nil }}
	router, readiness := testRouter(t, ok)
	if rec := do(router, "GET", "/health/live", "", ""); rec.Code != 200 {
		t.Fatalf("live = %d", rec.Code)
	}
	rec := do(router, "GET", "/health/ready", "", "")
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != 200 || body["status"] != "UP" {
		t.Fatalf("ready = %d %s", rec.Code, rec.Body)
	}
	readiness.StartDraining()
	if rec := do(router, "GET", "/health/ready", "", ""); rec.Code != 503 {
		t.Fatalf("ready while draining = %d", rec.Code)
	}
	if rec := do(router, "GET", "/metrics", "", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "http_requests_total") {
		t.Fatalf("metrics = %d", rec.Code)
	}
}

func TestReadinessDown(t *testing.T) {
	down := health.Check{Name: "postgres", Probe: func(context.Context) error { return errors.New("x") }}
	router, _ := testRouter(t, down)
	if rec := do(router, "GET", "/health/ready", "", ""); rec.Code != 503 {
		t.Fatalf("ready = %d", rec.Code)
	}
}

func TestCorrelationID(t *testing.T) {
	router, _ := testRouter(t)
	rec := do(router, "GET", "/health/live", "", "", "X-Correlation-Id", "corr-123")
	if rec.Header().Get("X-Correlation-Id") != "corr-123" {
		t.Fatalf("correlation not echoed: %q", rec.Header().Get("X-Correlation-Id"))
	}
	if rec := do(router, "GET", "/health/live", "", ""); rec.Header().Get("X-Correlation-Id") == "" {
		t.Fatal("a correlation id must be generated")
	}
	long := strings.Repeat("x", 200)
	if rec := do(router, "GET", "/health/live", "", "", "X-Correlation-Id", long); rec.Header().Get("X-Correlation-Id") == long {
		t.Fatal("oversized correlation ids must be replaced")
	}
}

func TestAuthMiddleware(t *testing.T) {
	router, _ := testRouter(t)
	for name, tc := range map[string]struct {
		header string
		status int
	}{
		"missing":     {"", 401},
		"basic":       {"Basic abc", 401},
		"unknown":     {"Bearer nope", 401},
		"wrong scope": {"Bearer provider-a", 403},
	} {
		t.Run(name, func(t *testing.T) {
			rec := do(router, "GET", "/wallets/0192f291-27dd-7d3f-8071-5f8685deef37", "", "", "Authorization", tc.header)
			if rec.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.status, rec.Body)
			}
			if tc.status == 401 && !strings.HasPrefix(rec.Header().Get("WWW-Authenticate"), "Bearer") {
				t.Fatal("401 must carry WWW-Authenticate: Bearer")
			}
		})
	}
}

func TestPanicIsLoggedAndMeasured(t *testing.T) {
	m := metrics.New()
	router := NewRouter(RouterDeps{
		Auth: authtest.Default(), Metrics: m, Readiness: health.NewReadiness(nil), Log: slog.New(slog.DiscardHandler),
	})
	router.GET("/panic-test", func(*gin.Context) { panic("x") })
	rec := do(router, "GET", "/panic-test", "", "")
	var body errorBody
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != 500 || body.Error.FailureCode != "INTERNAL_ERROR" {
		t.Fatalf("panic = %d %s", rec.Code, rec.Body)
	}
	if got := testutil.ToFloat64(m.HTTPRequests.WithLabelValues("GET", "/panic-test", "500")); got != 1 {
		t.Fatalf("http_requests_total = %v, want 1", got)
	}
}

func TestUnknownMethodIsBoundedInMetrics(t *testing.T) {
	m := metrics.New()
	router := NewRouter(RouterDeps{
		Auth: authtest.Default(), Metrics: m, Readiness: health.NewReadiness(nil), Log: slog.New(slog.DiscardHandler),
	})
	do(router, "FOOBAR", "/nope", "", "")
	if got := testutil.ToFloat64(m.HTTPRequests.WithLabelValues("OTHER", "unmatched", "404")); got != 1 {
		t.Fatalf("OTHER/unmatched = %v, want 1", got)
	}
}

func TestWriteErrorTransientSetsRetryAfter(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("GET", "/x", nil)
	writeError(c, app.ErrTransient)
	if rec.Code != 503 || rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("transient = %d Retry-After=%q", rec.Code, rec.Header().Get("Retry-After"))
	}
}

func TestUnknownRouteAnswersContractNotFound(t *testing.T) {
	router, _ := testRouter(t)
	rec := do(router, "GET", "/nope", "", "")
	var body errorBody
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != 404 || body.Error.FailureCode != "NOT_FOUND" {
		t.Fatalf("GET /nope = %d %s", rec.Code, rec.Body)
	}
}
