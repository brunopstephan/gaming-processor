//go:build !unit && !e2e

package app_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/apptest"
)

type recordingPublisher struct {
	mu   sync.Mutex
	fail error
	sent map[uuid.UUID]int
}

func (p *recordingPublisher) Publish(_ context.Context, e app.OutboxEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail != nil {
		return p.fail
	}
	if p.sent == nil {
		p.sent = map[uuid.UUID]int{}
	}
	p.sent[e.ID]++
	return nil
}

func newRelay(t *testing.T, h *apptest.Harness, pub app.EventPublisher, owner string, lease time.Duration) *app.OutboxRelay {
	t.Helper()
	r, err := app.NewOutboxRelay(h.Outbox, pub, h.Deps.Clock, h.Metrics, slog.New(slog.DiscardHandler), app.RelaySettings{
		Owner: owner, BatchSize: 50, Lease: lease, RetryBaseDelay: time.Second, RetryMaxDelay: 4 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// produce commits n BETs on fresh wallets and returns the ids of every event written.
func produce(t *testing.T, h *apptest.Harness, n int) []uuid.UUID {
	t.Helper()
	ws, _ := app.NewWageringService(h.Deps, wagering.DefaultRetryPolicy())
	for range n {
		w := h.OpenWallet(t, "100.00")
		if _, err := ws.Process(context.Background(), apptest.Command(t, w, "provider-a", "BET", "1.00", ""), app.Meta{}, nil); err != nil {
			t.Fatal(err)
		}
	}
	var ids []uuid.UUID
	if err := h.DB.Raw(`SELECT id FROM outbox_events ORDER BY occurred_at, id`).Scan(&ids).Error; err != nil {
		t.Fatal(err)
	}
	return ids
}

type outboxState struct {
	PublishedAt *time.Time
	LockedBy    *string
	Attempts    int
	LastError   *string
	NextAttempt time.Time `gorm:"column:next_attempt_at"`
}

func stateOf(t *testing.T, h *apptest.Harness, id uuid.UUID) outboxState {
	t.Helper()
	var s outboxState
	if err := h.DB.Raw(`SELECT published_at, locked_by, attempts, last_error, next_attempt_at FROM outbox_events WHERE id = ?`, id).Scan(&s).Error; err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRelayPublishesCommittedEventsOnce(t *testing.T) {
	h := apptest.NewIsolated(t)
	ids := produce(t, h, 3) // OPENING + BET events
	pub := &recordingPublisher{}
	relay := newRelay(t, h, pub, "relay-a", 30*time.Second)
	n, err := relay.RunOnce(context.Background())
	if err != nil || n != len(ids) {
		t.Fatalf("RunOnce = %d %v, want %d", n, err, len(ids))
	}
	for _, id := range ids {
		if pub.sent[id] != 1 || stateOf(t, h, id).PublishedAt == nil {
			t.Fatalf("event %s published %d times, state %+v", id, pub.sent[id], stateOf(t, h, id))
		}
	}
	if n, err := relay.RunOnce(context.Background()); err != nil || n != 0 {
		t.Fatalf("second RunOnce = %d %v, want nothing left", n, err)
	}
	if h.Metrics.Count("outbox:published") != len(ids) || h.Metrics.Count("outbox_lag") != len(ids) {
		t.Fatal("publish metrics missing")
	}
}

// ownerPublisher tags every publication with its relay and makes each relay's
// first publish wait until all relays hold a batch, so the claims provably overlap.
type ownerPublisher struct {
	owner   string
	rec     *recordingPublisher
	mu      *sync.Mutex
	by      map[uuid.UUID][]string
	ready   *sync.WaitGroup
	release chan struct{}
	once    sync.Once
	t       *testing.T
}

func (p *ownerPublisher) Publish(ctx context.Context, e app.OutboxEvent) error {
	p.once.Do(func() {
		p.ready.Done()
		select {
		case <-p.release:
		case <-time.After(10 * time.Second):
			p.t.Error("relays never overlapped")
		}
	})
	p.mu.Lock()
	p.by[e.ID] = append(p.by[e.ID], p.owner)
	p.mu.Unlock()
	return p.rec.Publish(ctx, e)
}

func TestConcurrentRelaysClaimDisjointEvents(t *testing.T) {
	h := apptest.NewIsolated(t)
	ids := produce(t, h, 15)
	rec := &recordingPublisher{}
	var mu sync.Mutex
	by := map[uuid.UUID][]string{}
	owners := []string{"relay-a", "relay-b", "relay-c"}
	var ready sync.WaitGroup
	ready.Add(len(owners))
	release := make(chan struct{})
	start := make(chan struct{})
	go func() { ready.Wait(); close(release) }()
	var wg sync.WaitGroup
	for _, o := range owners {
		pub := &ownerPublisher{owner: o, rec: rec, mu: &mu, by: by, ready: &ready, release: release, t: t}
		r, err := app.NewOutboxRelay(h.Outbox, pub, h.Deps.Clock, h.Metrics, slog.New(slog.DiscardHandler), app.RelaySettings{
			Owner: o, BatchSize: 2, Lease: 30 * time.Second, RetryBaseDelay: time.Second, RetryMaxDelay: 4 * time.Second,
		})
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for {
				n, err := r.RunOnce(context.Background())
				if err != nil {
					t.Error(err)
					return
				}
				if n == 0 {
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	publishers := map[string]bool{}
	for _, id := range ids {
		if len(by[id]) != 1 || rec.sent[id] != 1 {
			t.Fatalf("event %s published by %v (%d sends), want exactly once", id, by[id], rec.sent[id])
		}
		publishers[by[id][0]] = true
	}
	if len(publishers) < 2 {
		t.Fatalf("only %v published; no contention", publishers)
	}
}

func TestPublishFailureIsRescheduled(t *testing.T) {
	h := apptest.NewIsolated(t)
	ids := produce(t, h, 1)
	pub := &recordingPublisher{fail: errors.New("sqs unavailable")}
	relay := newRelay(t, h, pub, "relay-a", 30*time.Second)
	if _, err := relay.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := stateOf(t, h, ids[0])
	if s.PublishedAt != nil || s.LockedBy != nil || s.Attempts != 1 || s.LastError == nil || *s.LastError != "sqs unavailable" ||
		!s.NextAttempt.After(time.Now().Add(-time.Second)) {
		t.Fatalf("state after failure = %+v", s)
	}
	if n, _ := relay.RunOnce(context.Background()); n != 0 {
		t.Fatal("a rescheduled event must wait for its next attempt")
	}
	pub.fail = nil
	time.Sleep(1200 * time.Millisecond)
	if n, err := relay.RunOnce(context.Background()); err != nil || n != len(ids) || stateOf(t, h, ids[0]).PublishedAt == nil {
		t.Fatalf("retry = %d %v", n, err)
	}
	if h.Metrics.Count("outbox:failed") < 1 {
		t.Fatal("failed attempts not counted")
	}
}

// claimOnly simulates a relay that crashes after publishing, before marking.
type claimOnly struct{ app.OutboxRelayRepository }

func (claimOnly) MarkPublished(context.Context, uuid.UUID, string) (bool, error) {
	return false, errors.New("process died")
}

func TestExpiredLeaseIsRepublishedWithSameEventID(t *testing.T) {
	h := apptest.NewIsolated(t)
	ids := produce(t, h, 1)
	pub := &recordingPublisher{}
	crashed, err := app.NewOutboxRelay(claimOnly{h.Outbox}, pub, h.Deps.Clock, h.Metrics, slog.New(slog.DiscardHandler),
		app.RelaySettings{Owner: "relay-a", BatchSize: 50, Lease: time.Second, RetryBaseDelay: time.Second, RetryMaxDelay: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := crashed.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	survivor := newRelay(t, h, pub, "relay-b", 30*time.Second)
	if n, _ := survivor.RunOnce(context.Background()); n != 0 {
		t.Fatal("a leased event must not be claimed before its lease expires")
	}
	time.Sleep(1200 * time.Millisecond)
	if n, err := survivor.RunOnce(context.Background()); err != nil || n != len(ids) {
		t.Fatalf("after lease = %d %v", n, err)
	}
	for _, id := range ids {
		s := stateOf(t, h, id)
		if pub.sent[id] != 2 || s.PublishedAt == nil || s.Attempts != 2 {
			t.Fatalf("event %s sent %d times, state %+v; want republished with the same id", id, pub.sent[id], s)
		}
		if ok, err := h.Outbox.MarkPublished(context.Background(), id, "relay-a"); err != nil || ok {
			t.Fatalf("stale owner mark = %v %v, want false", ok, err)
		}
	}
}

func TestRescheduleStripsNULFromError(t *testing.T) {
	h := apptest.NewIsolated(t)
	ids := produce(t, h, 1)
	pub := &recordingPublisher{fail: errors.New("bad\x00byte")}
	relay := newRelay(t, h, pub, "relay-a", 30*time.Second)
	if _, err := relay.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := stateOf(t, h, ids[0])
	if s.LockedBy != nil || s.LastError == nil || *s.LastError != "badbyte" {
		t.Fatalf("state = %+v, want rescheduled with NUL stripped", s)
	}
}
