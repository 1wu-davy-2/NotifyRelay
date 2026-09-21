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
	// SkipReason names why the channel was never called: breaker_open,
	// quota_exhausted or rate_limited. Empty for an attempt that reached the
	// channel.
	//
	// It is stored rather than folded into Detail because the class cannot
	// carry the distinction — a skipped delivery and an unreachable one are
	// both CONNECT_ERROR — and an operator reading the trail has to be able to
	// tell "the channel is down" from "the budget ran out" without parsing
	// prose.
	SkipReason string
	ElapsedMS  int64
	CreatedAt  time.Time
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

// ChannelInstance is one configured channel, as persisted.
//
// It exists so that channel configuration can live in the database rather than
// in a file: the file has to be edited by hand and the service restarted, which
// is not a thing to ask of whoever is on call at three in the morning.
//
// Config holds the channel's own parameter block verbatim. The store does not
// interpret it — not the parameter names, and not which of them are sealed —
// because the schema that knows those things lives with the channel
// implementation, and a store that understood it would have to be updated every
// time a channel was added. Sealing is the caller's job; see internal/config.
type ChannelInstance struct {
	Name    string
	Type    string
	Enabled bool
	Config  map[string]any
	Quota   Quota
	// CreatedAt and UpdatedAt are set by the store, not by the caller.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Quota is a channel instance's send allowance. Zero means unlimited.
//
// It is a copy of the configuration shape rather than an import of it: the
// config package loads channel instances out of this store, so the store cannot
// depend on config without a cycle. Five integers are cheaper than a cycle.
type Quota struct {
	PerSecond int
	PerMinute int
	PerHour   int
	PerDay    int
	PerMonth  int
}

// Meta stores small facts about the database itself, as opposed to
// configuration or delivery state.
//
// It is deliberately untyped: what goes in it is a decision for whoever needs
// it, and a store that grew a field every time something needed remembering
// would be a store that changes shape for reasons that have nothing to do with
// persistence.
type Meta interface {
	// GetMeta returns a value, and whether it was set.
	GetMeta(ctx context.Context, key string) (string, bool, error)
	// SetMeta writes a value.
	SetMeta(ctx context.Context, key, value string) error
}

// MetaKeys are the facts this service keeps.
const (
	// MetaChannelsImported records that the configuration file's `channels:`
	// block has been copied into the database.
	//
	// Its presence, not the emptiness of the channel table, is what stops a
	// second import: an operator who deletes every channel must not find them
	// all back after a restart.
	MetaChannelsImported = "channels_imported_at"
)

// Channels stores the configured channel instances.
type Channels interface {
	// ListChannels returns every configured instance, by name.
	ListChannels(ctx context.Context) ([]*ChannelInstance, error)

	// GetChannel returns one instance, or nil when there is no such instance.
	GetChannel(ctx context.Context, name string) (*ChannelInstance, error)

	// PutChannel creates or replaces an instance.
	PutChannel(ctx context.Context, ci *ChannelInstance) error

	// DeleteChannel removes an instance, reporting whether it existed.
	DeleteChannel(ctx context.Context, name string) (bool, error)

	// CountChannels reports how many instances exist.
	CountChannels(ctx context.Context) (int, error)
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
	// skipReason names what had no capacity — breaker_open, quota_exhausted,
	// rate_limited — and is empty when the channel was called and could not be
	// reached. The two cases release identically but mean different things to
	// whoever is looking at the queue.
	//
	// next is when to try again. Releasing as immediately due would spin: the
	// channel is still down, so every attempt returns the same answer as fast
	// as the worker can ask.
	Release(ctx context.Context, id string, reason, skipReason string, next time.Time) (bool, error)

	// RecoverOrphans returns deliveries whose claim has expired back to the
	// queue, and reports how many it moved.
	//
	// claimTimeout is how long a claim is good for. A delivery claimed longer
	// ago than that is treated as abandoned whether or not anything crashed —
	// which is what makes the scan independent of a restart.
	RecoverOrphans(ctx context.Context, claimTimeout time.Duration, now time.Time) (int, error)

	// Replay returns a dead-lettered delivery to the queue with its attempt
	// budget restored, and reports whether it was moved.
	//
	// It refuses anything that is not dead-lettered. A sent delivery has
	// already arrived, and putting it back would deliver a second copy to real
	// people — the one outcome worse than the notification being late.
	Replay(ctx context.Context, id string, now time.Time) (bool, error)

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
	Channels
	Meta
	AdminAudit

	// Ping reports whether the store is usable, for the readiness probe.
	Ping(ctx context.Context) error

	// Close releases the store's resources.
	Close() error
}

// AdminAction is one thing an operator did through the admin surface.
//
// It is recorded because the actions worth auditing are the ones that turn off
// an automatic protection: resetting a breaker, deleting a channel, pointing
// one at a different endpoint. Those are all reasonable things to do, and all
// things somebody asks about afterwards. A log line answers the question only
// if the log still exists; a row answers it from the UI.
type AdminAction struct {
	ID     int64
	At     time.Time
	Actor  string // the operator's username
	Action string // "breaker.reset", "channel.save", "channel.delete"
	Target string // the channel name, where there is one
	Detail string // what changed, in one line
}

// AdminAudit stores the operator action trail.
type AdminAudit interface {
	// RecordAdminAction appends an entry.
	RecordAdminAction(ctx context.Context, a *AdminAction) error
	// ListAdminActions returns the most recent entries, newest first.
	ListAdminActions(ctx context.Context, limit int) ([]*AdminAction, error)
	// PruneAdminActions deletes entries older than the cutoff.
	PruneAdminActions(ctx context.Context, before time.Time) (int, error)
}

// Replayable reports whether a delivery can be put back in the queue.
//
// Only a dead-lettered delivery can. A sent one has already arrived, and
// replaying it would send a second copy — which is the one outcome worse than
// the notification being late.
func (s Status) Replayable() bool { return s == StatusFailed }
