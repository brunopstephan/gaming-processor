package httpapi

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/adapters/auth"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/metrics"
)

const (
	correlationHeader = "X-Correlation-Id"
	ctxCorrelation    = "correlationId"
	ctxPrincipal      = "principal"
	ctxLogger         = "logger"
)

// withCorrelation accepts a sane X-Correlation-Id or generates one, and echoes it.
func withCorrelation(c *gin.Context) {
	id := c.GetHeader(correlationHeader)
	if !validCorrelation(id) {
		id = uuid.Must(uuid.NewV7()).String()
	}
	c.Set(ctxCorrelation, id)
	c.Header(correlationHeader, id)
	c.Next()
}

func validCorrelation(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if r > unicode.MaxASCII || !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

func correlationID(c *gin.Context) string { return c.GetString(ctxCorrelation) }

func withLogger(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) { c.Set(ctxLogger, log); c.Next() }
}

func logger(c *gin.Context) *slog.Logger {
	if l, ok := c.Get(ctxLogger); ok {
		return l.(*slog.Logger)
	}
	return slog.Default()
}

// accessLog writes one JSON line per request; never bodies or tokens.
func accessLog(c *gin.Context) {
	start := time.Now()
	c.Next()
	attrs := []any{
		"correlationId", correlationID(c), "method", c.Request.Method, "route", routeOf(c),
		"status", c.Writer.Status(), "latencyMs", time.Since(start).Milliseconds(),
	}
	if p, ok := principalIfAny(c); ok && p.ProviderID != "" {
		attrs = append(attrs, "providerId", p.ProviderID)
	}
	logger(c).InfoContext(c.Request.Context(), "http request", attrs...)
}

// instrument records request counts and latency per route template.
func instrument(m *metrics.Metrics) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		route := routeOf(c)
		m.HTTPRequests.WithLabelValues(c.Request.Method, route, strconv.Itoa(c.Writer.Status())).Inc()
		m.HTTPDuration.WithLabelValues(c.Request.Method, route).Observe(time.Since(start).Seconds())
	}
}

func routeOf(c *gin.Context) string {
	if r := c.FullPath(); r != "" {
		return r
	}
	return "unmatched"
}

// recoverJSON turns panics into a 500 contract body.
func recoverJSON(c *gin.Context) {
	defer func() {
		if r := recover(); r != nil {
			logger(c).ErrorContext(c.Request.Context(), "panic", "correlationId", correlationID(c), "panic", r)
			c.AbortWithStatusJSON(http.StatusInternalServerError, failure("INTERNAL_ERROR", "", "internal error"))
		}
	}()
	c.Next()
}

// authenticate requires a valid Bearer token.
func authenticate(a auth.Authenticator) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, ok := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
		if !ok || raw == "" {
			unauthorized(c)
			return
		}
		p, err := a.Authenticate(c.Request.Context(), raw)
		if err != nil {
			unauthorized(c)
			return
		}
		c.Set(ctxPrincipal, p)
		c.Next()
	}
}

func unauthorized(c *gin.Context) {
	c.Header("WWW-Authenticate", `Bearer realm="wallet"`)
	c.AbortWithStatusJSON(http.StatusUnauthorized, failure("UNAUTHENTICATED", "", "missing, invalid or expired access token"))
}

func forbidden(c *gin.Context, message string) {
	c.AbortWithStatusJSON(http.StatusForbidden, failure("FORBIDDEN", "", message))
}

// requireAnyScope lets the request through if the principal has one of scopes.
func requireAnyScope(scopes ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		p := principal(c)
		for _, s := range scopes {
			if p.Has(s) {
				c.Next()
				return
			}
		}
		forbidden(c, "insufficient scope")
	}
}

func principalIfAny(c *gin.Context) (auth.Principal, bool) {
	v, ok := c.Get(ctxPrincipal)
	if !ok {
		return auth.Principal{}, false
	}
	p, ok := v.(auth.Principal)
	return p, ok
}

func principal(c *gin.Context) auth.Principal {
	p, _ := principalIfAny(c)
	return p
}
