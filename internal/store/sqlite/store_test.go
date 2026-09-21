package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"notifyrelay/internal/store"
)

func newStore(t *testing.T) *Store {
	t.Helper()

	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func delivery(id, target string, at time.Time) *store.Delivery {
	return &store.Delivery{
		ID:            id,
		RequestID:     "req-" + id,
		Target:        target,
		ChannelType:   "test",
		Status:        store.StatusQueued,
		NextAttemptAt: at,
		CreatedAt:     at,
		UpdatedAt:     at,
	}
}

func mustEnqueue(t *testing.T, s *Store, items ...*store.Delivery) {
	t.Helper()
	if err := s.Enqueue(context.Background(), items); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
}

// ---------------------------------------------------------------------- claim

func TestClaim_ReturnsDueDeliveries(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	mustEnqueue(t, s,
		delivery("aaaa1", "oncall", now.Add(-time.Minute)),
		delivery("aaaa2", "oncall", now.Add(-time.Second)),
		delivery("aaaa3", "oncall", now.Add(time.Hour)), // not due yet
	)

	got, err := s.Claim(ctx, 10, now)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("claimed %d deliveries, want 2", len(got))
	}
	// Oldest first: a backlog should drain in the order it arrived.
	if got[0].ID != "aaaa1" || got[1].ID != "aaaa2" {
		t.Errorf("claimed %s, %s; want aaaa1 then aaaa2", got[0].ID, got[1].ID)
	}
	for _, d := range got {
		if d.Status != store.StatusSending {
			t.Errorf("%s status = %s, want sending", d.ID, d.Status)
		}
	}
}

func TestClaim_RespectsTheLimit(t *testing.T) {
	s := newStore(t)
	now := time.Now().UTC()

	for i := 0; i < 10; i++ {
		d := delivery(fmt.Sprintf("aaaa%d", i), "oncall", now.Add(-time.Minute))
		mustEnqueue(t, s, d)
	}

	got, err := s.Claim(context.Background(), 3, now)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("claimed %d, want 3", len(got))
	}
}

// The failure this guards against is a duplicate notification: two workers
// reading the same due row and both believing it is theirs.
func TestClaim_ConcurrentClaimersNeverShareADelivery(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	const total = 200
	for i := 0; i < total; i++ {
		mustEnqueue(t, s, delivery(fmt.Sprintf("aaaa%03d", i), "oncall", now.Add(-time.Minute)))
	}

	const workers = 8
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		seen = map[string]int{}
	)

	start := make(chan struct{})
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for {
				claimed, err := s.Claim(ctx, 5, now)
				if err != nil {
					t.Errorf("Claim: %v", err)
					return
				}
				if len(claimed) == 0 {
					return
				}
				mu.Lock()
				for _, d := range claimed {
					seen[d.ID]++
				}
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()

	if len(seen) != total {
		t.Errorf("claimed %d distinct deliveries, want %d", len(seen), total)
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("delivery %s was claimed %d times; a delivery must go to exactly one worker", id, n)
		}
	}
}

func TestClaim_NothingDue(t *testing.T) {
	s := newStore(t)

	got, err := s.Claim(context.Background(), 10, time.Now().UTC())
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("claimed %d from an empty queue", len(got))
	}
}

// ------------------------------------------------------------------ outcomes

func TestComplete_MarksSentAndRecordsTheAttempt(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	mustEnqueue(t, s, delivery("aaaa1", "oncall", now.Add(-time.Minute)))
	claimed, _ := s.Claim(ctx, 1, now)

	attempt := &store.Attempt{
		DeliveryID: "aaaa1", RequestID: "req-aaaa1", Target: "oncall",
		ChannelType: "test", AttemptNo: 1, Class: "SENT", Detail: "delivered", ElapsedMS: 12,
	}
	if err := s.Complete(ctx, "aaaa1", attempt, now); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	d, err := s.Get(ctx, "aaaa1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if d.Status != store.StatusSent {
		t.Errorf("status = %s, want sent", d.Status)
	}
	if d.Attempts != 1 {
		t.Errorf("attempts = %d, want 1", d.Attempts)
	}
	if d.SentAt == nil {
		t.Error("sent_at was not recorded")
	}
	_ = claimed

	attempts, err := s.Attempts(ctx, "aaaa1")
	if err != nil {
		t.Fatalf("Attempts: %v", err)
	}
	if len(attempts) != 1 || attempts[0].Class != "SENT" {
		t.Errorf("attempts = %+v", attempts)
	}
}

func TestReschedule_SetsTheNextAttemptAndCounts(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	mustEnqueue(t, s, delivery("aaaa1", "oncall", now.Add(-time.Minute)))
	s.Claim(ctx, 1, now)

	next := now.Add(2 * time.Minute)
	attempt := &store.Attempt{
		DeliveryID: "aaaa1", AttemptNo: 1, Class: "TRANSIENT", Error: "peer said try later",
	}
	if err := s.Reschedule(ctx, "aaaa1", attempt, next); err != nil {
		t.Fatalf("Reschedule: %v", err)
	}

	d, _ := s.Get(ctx, "aaaa1")
	if d.Status != store.StatusQueued {
		t.Errorf("status = %s, want queued", d.Status)
	}
	if d.Attempts != 1 {
		t.Errorf("attempts = %d, want 1", d.Attempts)
	}
	if !d.NextAttemptAt.Equal(next) {
		t.Errorf("next attempt = %v, want %v", d.NextAttemptAt, next)
	}
	if d.LastError != "peer said try later" {
		t.Errorf("last error = %q", d.LastError)
	}

	// Not due until its time comes.
	if got, _ := s.Claim(ctx, 1, now); len(got) != 0 {
		t.Error("a rescheduled delivery was claimed before its next attempt time")
	}
	if got, _ := s.Claim(ctx, 1, next.Add(time.Second)); len(got) != 1 {
		t.Error("a rescheduled delivery was not claimed once due")
	}
}

func TestDeadLetter_EndsTheCycle(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	mustEnqueue(t, s, delivery("aaaa1", "oncall", now.Add(-time.Minute)))
	s.Claim(ctx, 1, now)

	attempt := &store.Attempt{DeliveryID: "aaaa1", AttemptNo: 5, Class: "PERMANENT", Error: "no such user"}
	if err := s.DeadLetter(ctx, "aaaa1", attempt, "no such user"); err != nil {
		t.Fatalf("DeadLetter: %v", err)
	}

	d, _ := s.Get(ctx, "aaaa1")
	if d.Status != store.StatusFailed {
		t.Errorf("status = %s, want failed", d.Status)
	}
	if !d.Status.Terminal() {
		t.Error("a dead-lettered delivery should be terminal")
	}

	// Never claimed again, however far the clock advances.
	if got, _ := s.Claim(ctx, 10, now.Add(365*24*time.Hour)); len(got) != 0 {
		t.Error("a dead-lettered delivery came back")
	}
}

// The no-capacity path: nothing was delivered and nothing was refused, so the
// message goes back to the queue without spending an attempt. Charging it
// would let one downstream outage exhaust every retry budget at once.
func TestRelease_DoesNotConsumeAnAttempt(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	mustEnqueue(t, s, delivery("aaaa1", "oncall", now.Add(-time.Minute)))
	s.Claim(ctx, 1, now)

	next := now.Add(30 * time.Second)
	released, err := s.Release(ctx, "aaaa1", "no channel capacity", "quota_exhausted", next)
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if !released {
		t.Fatal("Release reported nothing was released")
	}

	d, _ := s.Get(ctx, "aaaa1")
	if d.Status != store.StatusQueued {
		t.Errorf("status = %s, want queued", d.Status)
	}
	if d.Attempts != 0 {
		t.Errorf("attempts = %d, want 0 — a release must not spend the retry budget", d.Attempts)
	}
	if !d.NextAttemptAt.Equal(next) {
		t.Errorf("next attempt = %v, want %v", d.NextAttemptAt, next)
	}

	// It is still recorded, so the audit trail explains the gap.
	attempts, _ := s.Attempts(ctx, "aaaa1")
	if len(attempts) != 1 || attempts[0].Class != "RELEASED" {
		t.Errorf("attempts = %+v, want one RELEASED entry", attempts)
	}

	// Not due until the release delay has passed, or a down channel spins.
	if got, _ := s.Claim(ctx, 1, now); len(got) != 0 {
		t.Error("a released delivery was immediately claimable")
	}
	if got, _ := s.Claim(ctx, 1, next.Add(time.Second)); len(got) != 1 {
		t.Error("a released delivery was not claimable once due")
	}
}

// A worker that finished the job and crashed before recording it must not have
// its result overwritten by a late release.
func TestRelease_IgnoresADeliveryThatIsNoLongerInFlight(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	mustEnqueue(t, s, delivery("aaaa1", "oncall", now.Add(-time.Minute)))
	s.Claim(ctx, 1, now)
	s.Complete(ctx, "aaaa1", &store.Attempt{DeliveryID: "aaaa1", AttemptNo: 1, Class: "SENT"}, now)

	released, err := s.Release(ctx, "aaaa1", "late release", "", now.Add(time.Second))
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if released {
		t.Error("a completed delivery was released back into the queue")
	}

	d, _ := s.Get(ctx, "aaaa1")
	if d.Status != store.StatusSent {
		t.Errorf("status = %s, want sent", d.Status)
	}
}

// ------------------------------------------------------------------- recovery

// A kill -9 leaves rows stuck in "sending". They have to come back, or those
// notifications are silently lost.
func TestRecoverOrphans_ReturnsStuckDeliveriesToTheQueue(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	mustEnqueue(t, s, delivery("aaaa1", "oncall", now.Add(-time.Minute)))
	mustEnqueue(t, s, delivery("aaaa2", "oncall", now.Add(-time.Minute)))

	// Claim both, simulating a crash before either finished.
	claimed, _ := s.Claim(ctx, 10, now)
	if len(claimed) != 2 {
		t.Fatalf("claimed %d, want 2", len(claimed))
	}

	// Nothing has expired yet: a delivery claimed a moment ago may be in
	// flight in a worker that is perfectly healthy.
	s.SetClock(func() time.Time { return now })
	if n, err := s.RecoverOrphans(ctx, 5*time.Minute, now); err != nil || n != 0 {
		t.Errorf("recovered %d fresh in-flight deliveries (err=%v), want 0", n, err)
	}

	// Five minutes later the claim has expired.
	n, err := s.RecoverOrphans(ctx, 5*time.Minute, now.Add(6*time.Minute))
	if err != nil {
		t.Fatalf("RecoverOrphans: %v", err)
	}
	if n != 2 {
		t.Errorf("recovered %d, want 2", n)
	}

	for _, id := range []string{"aaaa1", "aaaa2"} {
		d, _ := s.Get(ctx, id)
		if d.Status != store.StatusQueued {
			t.Errorf("%s status = %s, want queued", id, d.Status)
		}
		if d.Attempts != 0 {
			t.Errorf("%s attempts = %d, want 0 — a crash is not the message's fault", id, d.Attempts)
		}
	}

	// And they are claimable again.
	if got, _ := s.Claim(ctx, 10, time.Now().UTC()); len(got) != 2 {
		t.Errorf("claimed %d after recovery, want 2", len(got))
	}
}

// ---------------------------------------------------------------- idempotency

func TestIdempotency_RoundTrip(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	if rec, err := s.GetRecord(ctx, "missing"); err != nil || rec != nil {
		t.Fatalf("GetRecord(missing) = %v, %v; want nil, nil", rec, err)
	}

	body := []byte(`{"request_id":"abc","results":[]}`)
	rec := &store.Record{Key: "k1", RequestID: "abc", Status: 200, Body: body, CreatedAt: time.Now().UTC()}
	if err := s.PutRecord(ctx, rec); err != nil {
		t.Fatalf("PutRecord: %v", err)
	}

	got, err := s.GetRecord(ctx, "k1")
	if err != nil {
		t.Fatalf("GetRecord: %v", err)
	}
	if got == nil || string(got.Body) != string(body) || got.Status != 200 || got.RequestID != "abc" {
		t.Errorf("record = %+v", got)
	}
}

// Two callers racing on the same key: the first response stands. Overwriting
// it would leave two callers holding different answers for one key.
func TestIdempotency_FirstRecordWins(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	first := &store.Record{Key: "k1", RequestID: "first", Status: 200, Body: []byte("first"), CreatedAt: time.Now().UTC()}
	second := &store.Record{Key: "k1", RequestID: "second", Status: 200, Body: []byte("second"), CreatedAt: time.Now().UTC()}

	if err := s.PutRecord(ctx, first); err != nil {
		t.Fatalf("PutRecord: %v", err)
	}
	if err := s.PutRecord(ctx, second); err != nil {
		t.Fatalf("PutRecord: %v", err)
	}

	got, _ := s.GetRecord(ctx, "k1")
	if got.RequestID != "first" {
		t.Errorf("record = %q, want the first one to survive", got.RequestID)
	}
}

// ------------------------------------------------------------------- breakers

func TestBreakers_RoundTrip(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	if st, err := s.LoadBreaker(ctx, "oncall"); err != nil || st != nil {
		t.Fatalf("LoadBreaker on an unknown channel = %v, %v; want nil, nil", st, err)
	}

	state := &store.BreakerState{
		Channel: "oncall", State: "open", Failures: 5,
		OpenedAt: time.Now().UTC(), Probes: 1, UpdatedAt: time.Now().UTC(),
	}
	if err := s.SaveBreaker(ctx, state); err != nil {
		t.Fatalf("SaveBreaker: %v", err)
	}

	got, err := s.LoadBreaker(ctx, "oncall")
	if err != nil {
		t.Fatalf("LoadBreaker: %v", err)
	}
	if got.State != "open" || got.Failures != 5 {
		t.Errorf("state = %+v", got)
	}

	// Saving again updates rather than duplicating.
	state.State = "closed"
	state.Failures = 0
	if err := s.SaveBreaker(ctx, state); err != nil {
		t.Fatalf("SaveBreaker: %v", err)
	}
	got, _ = s.LoadBreaker(ctx, "oncall")
	if got.State != "closed" {
		t.Errorf("state = %q, want closed", got.State)
	}
}

// --------------------------------------------------------------------- prune

func TestPrune_RemovesTerminalDeliveriesAndTheirAttempts(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	old := time.Now().UTC().Add(-48 * time.Hour)

	// Everything that happens "now" happens two days ago, so retention can be
	// exercised without waiting two days.
	s.SetClock(func() time.Time { return old })

	mustEnqueue(t, s,
		delivery("aaaa1", "oncall", old),
		delivery("aaaa2", "oncall", old),
		delivery("aaaa3", "oncall", old),
	)

	s.Claim(ctx, 10, old)
	s.Complete(ctx, "aaaa1", &store.Attempt{DeliveryID: "aaaa1", AttemptNo: 1, Class: "SENT"}, old)
	s.DeadLetter(ctx, "aaaa2", &store.Attempt{DeliveryID: "aaaa2", AttemptNo: 1, Class: "PERMANENT"}, "gone")

	// aaaa3 stays queued and must survive pruning.
	removed, err := s.Prune(ctx, time.Now().UTC().Add(-24*time.Hour), time.Now().UTC().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if removed != 2 {
		t.Errorf("pruned %d, want 2", removed)
	}

	if d, _ := s.Get(ctx, "aaaa3"); d == nil {
		t.Error("a queued delivery was pruned")
	}

	attempts, _ := s.Attempts(ctx, "aaaa1")
	if len(attempts) != 0 {
		t.Errorf("attempts survived their delivery: %+v", attempts)
	}
}

func TestPrune_KeepsRecentTerminalDeliveries(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	mustEnqueue(t, s, delivery("aaaa1", "oncall", now))
	s.Claim(ctx, 1, now)
	s.Complete(ctx, "aaaa1", &store.Attempt{DeliveryID: "aaaa1", AttemptNo: 1, Class: "SENT"}, now)

	removed, err := s.Prune(ctx, now.Add(-time.Hour), now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if removed != 0 {
		t.Errorf("pruned %d recent deliveries, want 0", removed)
	}
}

func TestList_FiltersAndOrders(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	mustEnqueue(t, s,
		delivery("aaaa1", "oncall", now.Add(-3*time.Minute)),
		delivery("aaaa2", "oncall", now.Add(-2*time.Minute)),
		delivery("aaaa3", "other", now.Add(-time.Minute)),
	)

	all, err := s.List(ctx, store.Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("listed %d, want 3", len(all))
	}
	// Newest first.
	if all[0].ID != "aaaa3" {
		t.Errorf("first result = %s, want the newest", all[0].ID)
	}

	byTarget, _ := s.List(ctx, store.Filter{Target: "other"})
	if len(byTarget) != 1 || byTarget[0].ID != "aaaa3" {
		t.Errorf("filtered by target = %+v", byTarget)
	}

	byRequest, _ := s.List(ctx, store.Filter{RequestID: "req-aaaa2"})
	if len(byRequest) != 1 || byRequest[0].ID != "aaaa2" {
		t.Errorf("filtered by request = %+v", byRequest)
	}
}

// The schema is applied with CREATE TABLE IF NOT EXISTS, which leaves an
// existing table exactly as it was. A column added to the DDL therefore never
// reaches a database created before it, and the first new column has to bring
// its own migration.
func TestOpen_AddsColumnsToAnExistingDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")

	// An attempts table as an earlier build wrote it: no skip_reason.
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	if _, err := raw.ExecContext(ctx, `
		CREATE TABLE attempts (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			delivery_id  TEXT    NOT NULL,
			request_id   TEXT    NOT NULL,
			target       TEXT    NOT NULL,
			channel_type TEXT    NOT NULL,
			attempt_no   INTEGER NOT NULL,
			class        TEXT    NOT NULL,
			detail       TEXT    NOT NULL DEFAULT '',
			error        TEXT    NOT NULL DEFAULT '',
			elapsed_ms   INTEGER NOT NULL DEFAULT 0,
			created_at   INTEGER NOT NULL
		)`); err != nil {
		t.Fatalf("create old attempts table: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	// Opening it must bring the column in rather than failing on the insert.
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open on an existing database: %v", err)
	}
	defer s.Close()

	has, err := s.hasColumn(ctx, "attempts", "skip_reason")
	if err != nil {
		t.Fatalf("hasColumn: %v", err)
	}
	if !has {
		t.Fatal("skip_reason was not added to the existing attempts table")
	}

	// And the row that motivated it round-trips.
	now := time.Now().UTC()
	d := delivery("mig1", "oncall", now)
	mustEnqueue(t, s, d)
	if _, err := s.Claim(ctx, 1, now); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, err := s.Release(ctx, "mig1", "no channel capacity", "quota_exhausted", now); err != nil {
		t.Fatalf("Release: %v", err)
	}

	got, err := s.Attempts(ctx, "mig1")
	if err != nil {
		t.Fatalf("Attempts: %v", err)
	}
	if len(got) != 1 || got[0].SkipReason != "quota_exhausted" {
		t.Fatalf("attempts = %+v, want the skip reason to survive the round trip", got)
	}
}

// Re-opening a migrated database must not try to add the column twice.
func TestOpen_MigrationIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nr.db")

	for i := 0; i < 3; i++ {
		s, err := Open(path)
		if err != nil {
			t.Fatalf("open %d: %v", i+1, err)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("close %d: %v", i+1, err)
		}
	}
}
