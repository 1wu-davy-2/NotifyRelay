// Package store defines the persistence the service needs.
//
// The interfaces are relational-shaped but not SQL-shaped. A claim returns
// rows and marks them in one step, which any relational engine can do —
// SQLite with a single producer, MySQL with SELECT ... FOR UPDATE SKIP LOCKED
// — even though the statements differ. Nothing above this package knows which
// engine is underneath.
//
// Message bodies are deliberately absent from these types. They live in spool
// files (see the spool package) so the database holds status and indexes
// rather than kilobytes of prose: a queue table that carries payloads grows
// without bound and every status update drags the payload along with it.
package store

import (
	"context"
	"time"
)

// Status is where a delivery is in its life.
type Status string

const (
	// StatusQueued is waiting for its next attempt.
	StatusQueued Status = "queued"
	// StatusSending has been claimed by a worker and is in flight.
	StatusSending Status = "sending"
	// StatusSent reached the channel.
	StatusSent Status = "sent"
	// StatusFailed exhausted its attempts and is dead-lettered.
	StatusFailed Status = "failed"
)

// Terminal reports whether no further attempt will be made.
func (s Status) Terminal() bool {
	return s == StatusSent || s == StatusFailed
}

// Delivery is one message bound for one channel instance.
type Delivery struct {
	ID          string
	RequestID   string
	Target      string // the channel alias, as the caller named it
	ChannelType string
	Status      Status
	Attempts    int
	NextAttemptAt time.Time
	LastError     string
	LastClass     string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	// ClaimedAt is when a worker took this delivery.
	//
	// It is separate from UpdatedAt on purpose: UpdatedAt answers "when did
	// this row last change", which is not the same question as "how long has
	// this been in flight". Using one field for both means a delivery that is
	// touched for any reason looks freshly claimed, and a stuck one is only
	// noticed by accident.
	ClaimedAt time.Time
	SentAt    *time.Time
}

// Attempt is one delivery attempt, kept for the audit trail.
//
// A row per attempt rather than a counter on the delivery: "how many times"
// answers nothing when something goes wrong, and the failure that matters is
// always the one whose detail was overwritten.
type Attempt struct {
	ID          int64
	DeliveryID  string
	RequestID   string
	Target      string
	ChannelType string
	AttemptNo   int
	Class       string // SENT | CONNECT_ERROR | TRANSIENT | PERMANENT | RELEASED
	Detail      string
	Error       string
	ElapsedMS   int64
	CreatedAt   time.Time
}

// Record is a stored response for an idempotency key.
//
// The whole response body is kept, not a flag saying "already handled": a
// caller that retries a request wants the same answer it would have got, not
// an acknowledgement that something happened once.
type Record struct {
	Key       string
	RequestID string
	Status    int
	Body      []byte
	CreatedAt time.Time
}

// BreakerState is a channel's circuit-breaker state.
//
// Persisted rather than kept in memory so that a restart does not forget an
// outage that is still in progress — the failure mode a restart is most
// likely to be part of.
type BreakerState struct {
	Channel     string
	State       string // closed | open | half_open
	Failures    int
	OpenedAt    time.Time
	Probes      int
	UpdatedAt   time.Time
}

// Filter narrows a delivery query.
type Filter struct {
	Status    Status
	Target    string
	RequestID string
	Limit     int
	Offset    int
}

// Queue holds deliveries waiting to go out.
type Queue interface {
	// Enqueue stores deliveries as ready to deliver.
	Enqueue(ctx context.Context, items []*Delivery) error

	// Claim takes up to limit due deliveries, atomically moving them to
	// StatusSending.
	//
	// It must be safe to call concurrently: a delivery must never be handed
	// to two workers, or a notification is sent twice. How that is achieved
	// is the implementation's business.
	Claim(ctx context.Context, limit int, now time.Time) ([]*Delivery, error)

	// Complete marks a delivery sent and records the attempt.
	Complete(ctx context.Context, id string, attempt *Attempt, at time.Time) error

	// Reschedule records a failed attempt and sets when to try again.
	Reschedule(ctx context.Context, id string, attempt *Attempt, next time.Time) error

	// DeadLetter ends the retry cycle and records why.
	DeadLetter(ctx context.Context, id string, attempt *Attempt, reason string) error

	// Release returns a claimed delivery to the queue without recording an
	// attempt, and reports whether the delivery was released.
	//
	// This is the no-capacity path: when nothing could accept the message,
	// charging it an attempt would let a downstream outage burn through every
	// message's retry budget and dump the lot into the dead-letter queue.
	//
	// next is when to try again. Releasing as immediately due would spin: the
	// channel is still down, so every attempt returns the same answer as fast
	// as the worker can ask.
	Release(ctx context.Context, id string, reason string, next time.Time) (bool, error)

	// RecoverOrphans returns deliveries whose claim has expired back to the
	// queue, and reports how many it moved.
	//
	// claimTimeout is how long a claim is good for. A delivery claimed longer
	// ago than that is treated as abandoned whether or not anything crashed —
	// which is what makes the scan independent of a restart.
	RecoverOrphans(ctx context.Context, claimTimeout time.Duration, now time.Time) (int, error)

	// Get returns one delivery, or nil when there is no such delivery.
	Get(ctx context.Context, id string) (*Delivery, error)

	// List returns deliveries, newest first.
	List(ctx context.Context, f Filter) ([]*Delivery, error)

	// Prune deletes terminal deliveries older than the cutoffs, and reports
	// how many it removed.
	Prune(ctx context.Context, sentBefore, failedBefore time.Time) (int, error)

	// Stats counts deliveries by status, for the queue-depth metric.
	Stats(ctx context.Context) (Stats, error)
}

// Stats is a snapshot of the queue.
type Stats struct {
	Queued  int
	Sending int
	Sent    int
	Failed  int
}

// Pending is the work still to do.
func (s Stats) Pending() int { return s.Queued + s.Sending }

// Audit stores the attempt history.
type Audit interface {
	// Attempts returns a delivery's attempts, oldest first.
	Attempts(ctx context.Context, deliveryID string) ([]*Attempt, error)

	// RecentAttempts returns the most recent attempts across all deliveries.
	RecentAttempts(ctx context.Context, limit int) ([]*Attempt, error)

	// PruneAttempts deletes attempts older than the cutoff.
	PruneAttempts(ctx context.Context, before time.Time) (int, error)
}

// Idempotency replays responses for repeated requests.
type Idempotency interface {
	// GetRecord returns the stored response for a key, or nil.
	GetRecord(ctx context.Context, key string) (*Record, error)
	// PutRecord stores a response for replay.
	PutRecord(ctx context.Context, rec *Record) error
	// PruneRecords deletes records older than the cutoff.
	PruneRecords(ctx context.Context, before time.Time) (int, error)
}

// Breakers persists circuit-breaker state.
type Breakers interface {
	// LoadBreaker returns a channel's state, or nil when it has none.
	LoadBreaker(ctx context.Context, channel string) (*BreakerState, error)
	// SaveBreaker writes a channel's state.
	SaveBreaker(ctx context.Context, st *BreakerState) error
}

// Quotas persists the long-window send allowances.
//
// Only the day and month windows are kept here. The shorter windows are held
// in memory by the quota limiter: losing a few seconds of counts costs
// nothing, and a row per second per channel would be all cost.
type Quotas interface {
	// TryConsumeCounter takes one unit from a window's allowance, reporting
	// whether there was room. The check and the increment must be atomic.
	TryConsumeCounter(ctx context.Context, channel, period string, limit int, now time.Time) (bool, error)

	// ReleaseCounter gives a unit back after a call that never reached the
	// peer.
	ReleaseCounter(ctx context.Context, channel, period string) error

	// Counter returns the current count for a window.
	Counter(ctx context.Context, channel, period string) (int, error)

	// PruneCounters deletes windows that ended before the cutoff.
	PruneCounters(ctx context.Context, before time.Time) (int, error)
}

// Store is everything the service persists.
type Store interface {
	Queue
	Audit
	Idempotency
	Breakers
	Quotas

	// Ping reports whether the store is usable, for the readiness probe.
	Ping(ctx context.Context) error

	// Close releases the store's resources.
	Close() error
}
