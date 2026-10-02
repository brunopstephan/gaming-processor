//go:build !unit && !e2e

package composition_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/brunopstephan/backend-challenge-go/internal/adapters/httpapi"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/composition"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/health"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/kctest"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/pgtest"
)

func setEnv(t *testing.T) {
	t.Setenv("DATABASE_URL", pgtest.AppURL())
	t.Setenv("OIDC_ISSUER_URL", kctest.IssuerURL())
	t.Setenv("HTTP_ADDR", "127.0.0.1:0")
	t.Setenv("HTTP_SHUTDOWN_TIMEOUT", "5s")
	t.Setenv("LOG_LEVEL", "error")
}

func TestCompositionGraphIsValid(t *testing.T) {
	setEnv(t)
	if err := fx.ValidateApp(fx.NopLogger, composition.Modules()); err != nil {
		t.Fatal(err)
	}
}

func TestServiceStartsServesAndDrains(t *testing.T) {
	pgtest.AppDB(t)
	setEnv(t)
	var (
		server    *httpapi.Server
		readiness *health.Readiness
	)
	app := fxtest.New(t, fx.NopLogger, composition.Modules(), fx.Populate(&server, &readiness))
	app.RequireStart()

	base := "http://" + server.Addr()
	for _, path := range []string{"/health/live", "/health/ready"} {
		resp, err := http.Get(base + path)
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("GET %s: %v %v", path, resp, err)
		}
		resp.Body.Close()
	}
	resp, err := http.Post(base+"/wallets", "application/json", nil)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("business endpoints require a token: %v %v", resp, err)
	}
	resp.Body.Close()

	app.RequireStop()
	if ok, results := readiness.Check(context.Background()); ok || results["draining"] != "true" {
		t.Fatalf("readiness after stop = %v %v (must be draining)", ok, results)
	}
	client := http.Client{Timeout: time.Second}
	if _, err := client.Get(base + "/health/live"); err == nil {
		t.Fatal("server still accepting connections after stop")
	}
}
