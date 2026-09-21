// Package breaker implements the per-channel circuit breaker.
//
// The failure that motivates it: a channel goes down, every delivery to it
// fails, and each one waits out a full connect timeout first. The queue stops
// draining and the workers spend their time on an endpoint that is already
// known to be down. A breaker turns that into an immediate, cheap answer, and
// keeps a trickle of probes going so recovery is noticed rather than waited
// for.
package breaker

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"notifyrelay/internal/channel"
	"notifyrelay/internal/store"
)

// State is where a channel's breaker is.
type State string

const (
	// StateClosed lets everything through.
	StateClosed State = "closed"
	// StateOpen lets nothing through until the open timeout elapses.
	StateOpen State = "open"
	// StateHalfOpen admits a limited number of probes.
	StateHalfOpen State = "half_open"
)

// Settings are the thresholds, from configuration.
type Settings struct {
	// FailureThreshold is how many consecutive failures open the breaker.
	FailureThreshold int
	// SuccessThreshold is how many half-open probes must succeed to close it.
	SuccessThreshold int
	// OpenTimeout is how long to stay open before probing.
	OpenTimeout time.Duration
	// HalfOpenProbes is how many probes may be in flight at once while
	// half-open.
	//
	// Limited rather than unlimited: a channel that has just come back should
	// be tested, not handed the backlog. If the backlog arrived at once and
	// the channel was still unwell, it would fall over again under the load
	// that a slower ramp would have survived.
	HalfOpenProbes int
}

// DefaultSettings is the failure profile of a channel that is in trouble.
func DefaultSettings() Settings {
	return Settings{
		FailureThreshold: 5,
		SuccessThreshold: 2,
		OpenTimeout:      60 * time.Second,
		HalfOpenProbes:   1,
	}
}

// Normalize fills in anything unset.
func (s Settings) Normalize() Settings {
	d := DefaultSettings()
	if s.FailureThreshold <= 0 {
		s.FailureThreshold = d.FailureThreshold
	}
	if s.SuccessThreshold <= 0 {
		s.SuccessThreshold = d.SuccessThreshold
	}
	if s.OpenTimeout <= 0 {
		s.OpenTimeout = d.OpenTimeout
	}
	if s.HalfOpenProbes <= 0 {
		s.HalfOpenProbes = d.HalfOpenProbes
	}
	return s
}

// Store is where breaker state is written through. The store package's
// Breakers interface satisfies it.
type Store interface {
	LoadBreaker(ctx context.Context, channel string) (*store.BreakerState, error)
	SaveBreaker(ctx context.Context, st *store.BreakerState) error
}

// Breaker tracks one channel's health.
type Breaker struct {
	channel  string
	settings Settings
	store    Store
	log      *slog.Logger

	mu sync.Mutex
	// loaded is false until the persisted state has been read, so the first
	// decision after startup is made with the state the last run left behind
	// rather than with a fresh, closed breaker.
	loaded   bool
	state    State
	failures int
	openedAt time.Time
	probes   int
	successes int
}

// Manager holds one breaker per channel.
type Manager struct {
	settings Settings
	store    Store
	log      *slog.Logger

	mu       sync.Mutex
	breakers map[string]*Breaker
}

// NewManager builds a breaker manager. A nil store means state is kept in
// memory only, which is useful in tests and wrong in production.
func NewManager(settings Settings, store Store, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{
		settings: settings.Normalize(),
		store:    store,
		log:      log,
		breakers: map[string]*Breaker{},
	}
}

// For returns the breaker for a channel, creating it on first use.
func (m *Manager) For(channelName string) *Breaker {
	m.mu.Lock()
	defer m.mu.Unlock()

	b, ok := m.breakers[channelName]
	if !ok {
		b = &Breaker{
			channel:  channelName,
			settings: m.settings,
			store:    m.store,
			log:      m.log,
			state:    StateClosed,
		}
		m.breakers[channelName] = b
	}
	return b
}

// Allow reports whether a delivery to this channel may proceed.
//
// The second return value explains a refusal, for the audit trail.
func (b *Breaker) Allow(ctx context.Context, now time.Time) (bool, string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.load(ctx)

	switch b.state {
	case StateOpen:
		if now.Sub(b.openedAt) < b.settings.OpenTimeout {
			return false, fmt.Sprintf("channel is unhealthy; retrying after %s",
				b.settings.OpenTimeout)
		}
		// The open timeout has elapsed: admit probes.
		b.transition(ctx, StateHalfOpen, now)

	case StateHalfOpen:
		if b.probes >= b.settings.HalfOpenProbes {
			return false, "channel is being probed"
		}
	}

	if b.state == StateHalfOpen {
		b.probes++
	}
	return true, ""
}

// Record reports the outcome of a delivery.
//
// Only TRANSIENT and CONNECT_ERROR count as channel faults. A PERMANENT
// rejection is the message's problem, not the channel's: the endpoint answered,
// and it answered correctly. Counting it would let one bad recipient address
// take the whole channel out of service.
func (b *Breaker) Record(ctx context.Context, class channel.ResultClass, now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.load(ctx)

	switch class {
	case channel.ClassSent, channel.ClassPermanent:
		b.recordSuccess(ctx, now)
	case channel.ClassTransient, channel.ClassConnectError:
		b.recordFailure(ctx, now)
	}
}

func (b *Breaker) recordSuccess(ctx context.Context, now time.Time) {
	switch b.state {
	case StateHalfOpen:
		if b.probes > 0 {
			b.probes--
		}
		b.successes++
		if b.successes >= b.settings.SuccessThreshold {
			b.log.Info("breaker: channel recovered",
				slog.String("channel", b.channel),
				slog.Int("probes", b.successes),
			)
			b.transition(ctx, StateClosed, now)
		}

	case StateClosed:
		b.failures = 0
	}
}

func (b *Breaker) recordFailure(ctx context.Context, now time.Time) {
	switch b.state {
	case StateHalfOpen:
		// A probe failed, so the channel is not back. Straight back to open
		// rather than collecting more evidence: the evidence is that the last
		// attempt failed.
		if b.probes > 0 {
			b.probes--
		}
		b.log.Warn("breaker: probe failed, reopening",
			slog.String("channel", b.channel))
		b.transition(ctx, StateOpen, now)

	case StateClosed:
		b.failures++
		if b.failures >= b.settings.FailureThreshold {
			b.log.Warn("breaker: channel opened after consecutive failures",
				slog.String("channel", b.channel),
				slog.Int("failures", b.failures),
			)
			b.transition(ctx, StateOpen, now)
		}

	default:
		// Already open; nothing to do but keep the timestamp.
	}
}

// transition moves the breaker to a new state and writes it through.
func (b *Breaker) transition(ctx context.Context, next State, now time.Time) {
	b.state = next
	b.successes = 0
	b.probes = 0

	switch next {
	case StateOpen:
		b.openedAt = now
	case StateClosed:
		b.failures = 0
		b.openedAt = time.Time{}
	case StateHalfOpen:
		b.probes = 0
	}

	b.persist(ctx, now)
}

// load reads the persisted state once.
//
// This is what makes a restart remember an outage. Without it the process
// comes back believing every channel is healthy and sends the whole backlog
// straight into an endpoint that is still down — the restart being, quite
// often, part of the incident rather than the end of it.
func (b *Breaker) load(ctx context.Context) {
	if b.loaded || b.store == nil {
		b.loaded = true
		return
	}
	b.loaded = true

	rec, err := b.store.LoadBreaker(ctx, b.channel)
	if err != nil {
		b.log.Warn("breaker: could not read persisted state",
			slog.String("channel", b.channel), slog.String("error", err.Error()))
		return
	}
	if rec == nil || rec.State == "" {
		return
	}

	b.state = State(rec.State)
	b.failures = rec.Failures
	b.openedAt = rec.OpenedAt
	// Probes from a previous run are not still in flight.
	b.probes = 0

	if b.state == StateOpen {
		b.log.Warn("breaker: channel is still open from a previous run",
			slog.String("channel", b.channel),
			slog.Time("opened_at", b.openedAt),
		)
	}
}

// persist writes the state through to the store.
func (b *Breaker) persist(ctx context.Context, now time.Time) {
	if b.store == nil {
		return
	}

	err := b.store.SaveBreaker(ctx, &store.BreakerState{
		Channel:   b.channel,
		State:     string(b.state),
		Failures:  b.failures,
		OpenedAt:  b.openedAt,
		Probes:    b.probes,
		UpdatedAt: now,
	})
	if err != nil {
		b.log.Warn("breaker: could not persist state",
			slog.String("channel", b.channel), slog.String("error", err.Error()))
	}
}

// Snapshot returns the current state, for tests and the admin surface.
func (b *Breaker) Snapshot(ctx context.Context, now time.Time) (State, int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.load(ctx)
	return b.state, b.failures
}
