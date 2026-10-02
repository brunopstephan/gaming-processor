package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/fx"

	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/health"
)

// Server runs the HTTP API inside the Fx lifecycle.
type Server struct {
	srv *http.Server
	ln  net.Listener
}

// Addr is the bound address (valid after start; supports ":0").
func (s *Server) Addr() string {
	if s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

// NewServer binds on start (failing fast if the port is taken) and, on stop,
// marks readiness as draining, stops accepting connections and waits for
// in-flight requests up to HTTP_SHUTDOWN_TIMEOUT.
func NewServer(lc fx.Lifecycle, cfg config.Config, router *gin.Engine, readiness *health.Readiness, log *slog.Logger) *Server {
	s := &Server{srv: &http.Server{Handler: router, ReadHeaderTimeout: 5 * time.Second}}
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			ln, err := net.Listen("tcp", cfg.HTTP.Addr)
			if err != nil {
				return err
			}
			s.ln = ln
			go func() {
				if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
					log.Error("http server stopped", "error", err.Error())
				}
			}()
			log.Info("http server listening", "addr", s.Addr())
			return nil
		},
		OnStop: func(ctx context.Context) error {
			readiness.StartDraining()
			ctx, cancel := context.WithTimeout(ctx, cfg.HTTP.ShutdownTimeout)
			defer cancel()
			if err := s.srv.Shutdown(ctx); err != nil {
				// Deadline hit: force-close so handlers do not outlive the DB pool.
				_ = s.srv.Close()
				return err
			}
			return nil
		},
	})
	return s
}
