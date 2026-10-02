//go:build !unit && !e2e

package composition_test

import (
	"context"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"
	"go.uber.org/fx/fxtest"

	"github.com/brunopstephan/backend-challenge-go/internal/adapters/httpapi"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/composition"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/health"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/kctest"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/pgtest"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/sqstest"
)

func setEnv(t *testing.T) config.Config {
	q := sqstest.NewQueues(t, sqstest.Options{})
	for k, v := range map[string]string{
		"DATABASE_URL": pgtest.FreshDatabase(t), "OIDC_ISSUER_URL": kctest.IssuerURL(), "HTTP_ADDR": "127.0.0.1:0",
		"HTTP_SHUTDOWN_TIMEOUT": "5s", "LOG_LEVEL": "error", "AWS_ACCESS_KEY_ID": "test", "AWS_SECRET_ACCESS_KEY": "test",
		"SQS_ENDPOINT": sqstest.Endpoint(), "SQS_INPUT_QUEUE": q.Input.Name, "SQS_INPUT_DLQ": q.InputDLQ.Name,
		"SQS_EVENTS_QUEUE": q.Events.Name,
	} {
		t.Setenv(k, v)
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestCompositionGraphIsValid(t *testing.T) {
	cfg := setEnv(t)
	if err := fx.ValidateApp(fx.NopLogger, composition.Modules(cfg)); err != nil {
		t.Fatal(err)
	}
}

func TestServiceStartsServesAndDrains(t *testing.T) {
	cfg := setEnv(t)
	var (
		server    *httpapi.Server
		readiness *health.Readiness
	)
	app := fxtest.New(t, fx.NopLogger, composition.Modules(cfg), fx.Populate(&server, &readiness))
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

func TestToggleCombinationsValidate(t *testing.T) {
	cfg := setEnv(t)
	for name, toggles := range map[string]config.Toggles{
		"all":           {HTTP: true, Consumer: true, Outbox: true, RefWorker: true},
		"http only":     {HTTP: true},
		"consumer only": {Consumer: true},
		"workers only":  {Outbox: true, RefWorker: true},
	} {
		t.Run(name, func(t *testing.T) {
			c := cfg
			c.Toggles = toggles
			if !toggles.HTTP {
				c.Auth = config.Auth{} // a process without HTTP needs no IdP
			}
			if err := fx.ValidateApp(fx.NopLogger, composition.Modules(c)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// stopRecorder records the order in which Fx runs OnStop hooks. Fx reports
// each hook with the function that registered it (CallerName), which names
// the component without touching production code.
type stopRecorder struct {
	mu      sync.Mutex
	stopped []string
}

func (r *stopRecorder) LogEvent(ev fxevent.Event) {
	if e, ok := ev.(*fxevent.OnStopExecuting); ok {
		r.mu.Lock()
		r.stopped = append(r.stopped, e.CallerName)
		r.mu.Unlock()
	}
}

func TestHTTPStopsBeforeConsumerAndWorkers(t *testing.T) {
	cfg := setEnv(t)
	cfg.Toggles = config.Toggles{HTTP: true, Consumer: true, Outbox: true, RefWorker: true}
	rec := &stopRecorder{}
	app := fxtest.New(t, fx.WithLogger(func() fxevent.Logger { return rec }), composition.Modules(cfg))
	app.RequireStart()
	app.RequireStop()

	rec.mu.Lock()
	defer rec.mu.Unlock()
	index := func(part string) []int {
		var at []int
		for i, name := range rec.stopped {
			if strings.Contains(name, part) {
				at = append(at, i)
			}
		}
		return at
	}
	httpAt, consumerAt, workerAt := index("httpapi."), index("sqsconsumer."), index("background.")
	if len(httpAt) != 1 || len(consumerAt) != 1 || len(workerAt) != 2 {
		t.Fatalf("stop hooks = %v; want one http, one consumer and two background hooks", rec.stopped)
	}
	if httpAt[0] > consumerAt[0] || httpAt[0] > workerAt[0] {
		t.Fatalf("stop order = %v; HTTP must stop first (spec §13)", rec.stopped)
	}
}
