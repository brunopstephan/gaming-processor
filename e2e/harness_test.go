//go:build !unit && !integration

// Package e2e runs the real wallet binary as several OS processes against
// the test infrastructure (postgres_test, keycloak_test, ministack_test).
// TestMain builds the binary with the faultinject tag so recovery scenarios
// can crash a process at a named point (FAULT=...).
package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/kctest"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/pgtest"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/sqstest"
)

var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "wallet-e2e-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	binary = filepath.Join(dir, "wallet")
	_, file, _, _ := runtime.Caller(0)
	build := exec.Command("go", "build", "-race", "-tags", "faultinject", "-o", binary, "./cmd/wallet")
	build.Dir = filepath.Join(filepath.Dir(file), "..")
	build.Stdout, build.Stderr = os.Stderr, os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "build wallet binary:", err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

type clusterOptions struct {
	// VisibilityTimeout of the per-test input queue (seconds; 0 = 30).
	VisibilityTimeout int
	// Env applies to every process of the cluster.
	Env map[string]string
}

type cluster struct {
	dbURL  string
	db     *gorm.DB
	queues sqstest.Queues
	env    map[string]string
	procs  []*proc
}

type proc struct {
	name   string
	addr   string
	cmd    *exec.Cmd
	log    *bytes.Buffer
	logMu  *sync.Mutex
	exited chan struct{}
	code   int
}

// newCluster creates a fresh database and queues; processes are started with start.
func newCluster(t *testing.T, opts clusterOptions) *cluster {
	t.Helper()
	c := &cluster{dbURL: pgtest.FreshDatabase(t), queues: sqstest.NewQueues(t, sqstest.Options{VisibilityTimeout: opts.VisibilityTimeout})}
	c.db = pgtest.Open(t, c.dbURL)
	c.env = map[string]string{
		"PATH": os.Getenv("PATH"), "GORACE": "halt_on_error=1", "DB_MAX_OPEN_CONNS": "8",
		"AWS_CONFIG_FILE": filepath.Join(t.TempDir(), "none"), "AWS_SHARED_CREDENTIALS_FILE": filepath.Join(t.TempDir(), "none"),
		"DATABASE_URL": c.dbURL, "OIDC_ISSUER_URL": kctest.IssuerURL(), "LOG_LEVEL": "info",
		"AWS_ACCESS_KEY_ID": "test", "AWS_SECRET_ACCESS_KEY": "test", "AWS_REGION": sqstest.Region,
		"SQS_ENDPOINT": sqstest.Endpoint(), "SQS_INPUT_QUEUE": c.queues.Input.Name,
		"SQS_INPUT_DLQ": c.queues.InputDLQ.Name, "SQS_EVENTS_QUEUE": c.queues.Events.Name,
		"SQS_WAIT_TIME": "1s", "SQS_RETRY_BASE_DELAY": "1s", "SQS_RETRY_MAX_DELAY": "2s",
		"SQS_SHUTDOWN_TIMEOUT": "5s", "HTTP_SHUTDOWN_TIMEOUT": "5s",
		"OUTBOX_POLL_INTERVAL": "100ms", "REFWORKER_POLL_INTERVAL": "100ms",
		"OUTBOX_SHUTDOWN_TIMEOUT": "5s", "REFWORKER_SHUTDOWN_TIMEOUT": "5s",
	}
	for k, v := range opts.Env {
		c.env[k] = v
	}
	t.Cleanup(func() {
		for _, p := range c.procs {
			p.kill(t)
		}
		for _, p := range c.procs {
			if strings.Contains(p.tail(100000), "WARNING: DATA RACE") {
				t.Errorf("%s reported a data race", p.name)
			}
		}
		if t.Failed() {
			for _, p := range c.procs {
				t.Logf("---- %s log (tail) ----\n%s", p.name, p.tail(60))
			}
		}
	})
	return c
}

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

// start launches a process and waits until it is ready (or, with FAULT set,
// until it is ready or has crashed).
func (c *cluster) start(t *testing.T, name string, env map[string]string) *proc {
	t.Helper()
	p := &proc{name: name, addr: freePort(t), log: &bytes.Buffer{}, logMu: &sync.Mutex{}, exited: make(chan struct{})}
	all := map[string]string{"HTTP_ADDR": p.addr}
	for k, v := range c.env {
		all[k] = v
	}
	for k, v := range env {
		all[k] = v
	}
	p.cmd = exec.Command(binary)
	for k, v := range all {
		p.cmd.Env = append(p.cmd.Env, k+"="+v)
	}
	w := &lockedWriter{buf: p.log, mu: p.logMu}
	p.cmd.Stdout, p.cmd.Stderr = w, w
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		err := p.cmd.Wait()
		p.code = exitCode(err)
		close(p.exited)
	}()
	c.procs = append(c.procs, p)
	eventually(t, 30*time.Second, name+" ready", func() bool {
		select {
		case <-p.exited:
			t.Fatalf("%s exited with %d before becoming ready:\n%s", name, p.code, p.tail(40))
		default:
		}
		resp, err := probeClient.Get(p.url() + "/health/ready")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	})
	return p
}

type lockedWriter struct {
	buf *bytes.Buffer
	mu  *sync.Mutex
}

func (w *lockedWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(b)
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return ee.ExitCode()
	}
	return -1
}

func (p *proc) url() string { return "http://" + p.addr }

func (p *proc) tail(n int) string {
	p.logMu.Lock()
	defer p.logMu.Unlock()
	lines := strings.Split(strings.TrimRight(p.log.String(), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// stop sends SIGTERM and returns the exit code.
func (p *proc) stop(t *testing.T) int {
	t.Helper()
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	return p.waitExit(t, 60*time.Second)
}

// kill sends SIGKILL (no cleanup) unless the process already exited.
func (p *proc) kill(t *testing.T) {
	select {
	case <-p.exited:
		return
	default:
	}
	_ = p.cmd.Process.Kill()
	<-p.exited
}

func (p *proc) waitExit(t *testing.T, timeout time.Duration) int {
	t.Helper()
	select {
	case <-p.exited:
		return p.code
	case <-time.After(timeout):
		t.Fatalf("%s did not exit within %s", p.name, timeout)
		return -1
	}
}

// metric returns the sum of a counter (all label sets) from /metrics.
func (p *proc) metric(t *testing.T, name string) float64 {
	t.Helper()
	resp, err := probeClient.Get(p.url() + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	sum := 0.0
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, name) || strings.HasPrefix(line, "#") {
			continue
		}
		rest := strings.TrimPrefix(line, name)
		if rest != "" && rest[0] != ' ' && rest[0] != '{' {
			continue
		}
		fields := strings.Fields(line)
		v, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err == nil {
			sum += v
		}
	}
	return sum
}

func eventually(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out after %s waiting for %s", timeout, what)
}

// ---- HTTP and SQS helpers ----

type wallet struct{ id, player string }

var probeClient = &http.Client{Timeout: 5 * time.Second}

var httpClient = &http.Client{Timeout: 30 * time.Second}

func call(t *testing.T, method, url, token, body string, headers ...string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("%s %s: body %q: %v", method, url, raw, err)
		}
	}
	return resp.StatusCode, out
}

func openWallet(t *testing.T, p *proc, amount string) wallet {
	t.Helper()
	player := uuid.NewString()
	code, body := call(t, "POST", p.url()+"/wallets", kctest.Token(t, "wallet-internal"),
		fmt.Sprintf(`{"playerId":%q,"initialBalance":{"amount":%q,"currency":"BRL"}}`, player, amount))
	if code != http.StatusCreated {
		t.Fatalf("open wallet = %d %v", code, body)
	}
	return wallet{id: body["id"].(string), player: player}
}

type txRequest struct {
	Provider, Ext, Kind, Amount, Ref string
	W                                wallet
}

func (r txRequest) json() string {
	ref := ""
	if r.Ref != "" {
		ref = fmt.Sprintf(`,"referenceExternalTransactionId":%q`, r.Ref)
	}
	return fmt.Sprintf(`{"providerId":%q,"externalTransactionId":%q,"playerId":%q,"walletId":%q,"roundId":"round-1",`+
		`"gameId":"game-1","kind":%q,"money":{"amount":%q,"currency":"BRL"}%s}`,
		r.Provider, r.Ext, r.W.player, r.W.id, r.Kind, r.Amount, ref)
}

func postTx(t *testing.T, p *proc, token string, r txRequest) (int, map[string]any) {
	t.Helper()
	return call(t, "POST", p.url()+"/wagering/transactions", token, r.json(), "Idempotency-Key", r.Provider+":"+r.Ext)
}

// sqsBody is the input message for r (idempotency key provider:ext).
func sqsBody(msgID string, r txRequest) string {
	return fmt.Sprintf(`{"messageId":%q,"type":"WagerTransactionRequested","occurredAt":"2026-09-08T12:00:00.000Z",`+
		`"data":{"providerId":%q,"externalTransactionId":%q,"idempotencyKey":%q,"playerId":%q,"walletId":%q,`+
		`"roundId":"round-1","gameId":"game-1","kind":%q,"money":{"amount":%q,"currency":"BRL"}%s}}`,
		msgID, r.Provider, r.Ext, r.Provider+":"+r.Ext, r.W.player, r.W.id, r.Kind, r.Amount, refField(r.Ref))
}

func refField(ref string) string {
	if ref == "" {
		return ""
	}
	return fmt.Sprintf(`,"referenceExternalTransactionId":%q`, ref)
}

// sendSQS sends body as provider-a's account with a fresh deduplication id
// (so a resend is a new SQS message carrying the same messageId).
func (c *cluster) sendSQS(t *testing.T, body, group string) {
	t.Helper()
	sqstest.Send(t, sqstest.Client(t, sqstest.ProviderAAccount), c.queues.Input.URL, body, group)
}

func balance(t *testing.T, p *proc, w wallet) string {
	t.Helper()
	code, body := call(t, "GET", p.url()+"/wallets/"+w.id, kctest.Token(t, "wallet-internal"), "")
	if code != http.StatusOK {
		t.Fatalf("get wallet = %d %v", code, body)
	}
	return body["balance"].(map[string]any)["amount"].(string)
}

// reconcile requires stored balance == credits − debits of the ledger.
func reconcile(t *testing.T, p *proc, w wallet) {
	t.Helper()
	code, body := call(t, "POST", p.url()+"/wallets/"+w.id+"/reconciliation", kctest.Token(t, "wallet-internal"), "")
	if code != http.StatusOK || body["consistent"] != true {
		t.Fatalf("reconciliation of %s = %d %v", w.id, code, body)
	}
}

func getByExternal(t *testing.T, p *proc, provider, ext string) map[string]any {
	t.Helper()
	code, body := call(t, "GET", p.url()+"/providers/"+provider+"/wagering/transactions/"+ext, kctest.Token(t, provider), "")
	if code != http.StatusOK {
		return nil
	}
	return body
}

// queueEmpty reports whether the queue holds no visible or in-flight messages.
func (c *cluster) queueEmpty(t *testing.T, url string) bool {
	t.Helper()
	out, err := sqstest.Owner(t).GetQueueAttributes(context.Background(), &sqs.GetQueueAttributesInput{
		QueueUrl:       aws.String(url),
		AttributeNames: []sqstypes.QueueAttributeName{"ApproximateNumberOfMessages", "ApproximateNumberOfMessagesNotVisible"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return out.Attributes["ApproximateNumberOfMessages"] == "0" && out.Attributes["ApproximateNumberOfMessagesNotVisible"] == "0"
}

func stringsReader(s string) io.Reader { return strings.NewReader(s) }

func decodeBody(resp *http.Response) map[string]any {
	out := map[string]any{}
	raw, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{}
	}
	return out
}
