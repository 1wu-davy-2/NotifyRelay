// Package quota tracks a channel's send allowance and reserves against it
// before the channel is called.
//
// The reservation is the point. A counter that is incremented after the call
// has no answer for the call that never happened: a channel that was
// unreachable for an hour would spend an hour's allowance and then be refused
// for the rest of the day on the strength of messages nobody ever received.
// Reserving first and releasing on a failure that never reached the peer
// charges only for the calls the platform actually saw.
//
// This is a different job from the router's rate limiter. The limiter paces
// calls, spreading them so an endpoint is not hit in bursts it would reject.
// The quota decides how many calls are allowed at all, over windows measured
// in days, and remembers that across restarts.
package quota

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Limits is a channel's allowance. Zero means unlimited.
type Limits struct {
	PerSecond int
	PerMinute int
	PerHour   int
	PerDay    int
	PerMonth  int
}

// IsZero reports whether nothing is limited.
func (l Limits) IsZero() bool {
	return l.PerSecond == 0 && l.PerMinute == 0 && l.PerHour == 0 &&
		l.PerDay == 0 && l.PerMonth == 0
}

// Counters persists the long windows. A nil Counters keeps everything in
// memory, which is right for tests and wrong in production: the day and month
// windows are the ones a platform actually enforces.
type Counters interface {
	TryConsumeCounter(ctx context.Context, channel, period string, limit int, now time.Time) (bool, error)
	ReleaseCounter(ctx context.Context, channel, period string) error
}

// Limiter hands out reservations per channel.
type Limiter struct {
	counters Counters
	now      func() time.Time

	mu      sync.Mutex
	windows map[string]*channelWindows
}

// New builds a limiter.
func New(counters Counters) *Limiter {
	return &Limiter{
		counters: counters,
		now:      func() time.Time { return time.Now().UTC() },
		windows:  map[string]*channelWindows{},
	}
}

// SetClock replaces the limiter's clock. Intended for tests.
func (l *Limiter) SetClock(now func() time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.now = now
}

// Reservation is a claim on a channel's allowance.
//
// Exactly one of Commit or Release must be called. A reservation that is
// neither leaks a slot: it stays counted until its window rolls over, which is
// how a relay ends up mysteriously refusing to send.
type Reservation struct {
	limiter *Limiter
	channel string
	limits  Limits
	now     time.Time

	// short lists the in-memory windows this reservation was counted in.
	short []shortWindow

	// long lists the persisted windows, with the period key each used.
	long []longWindow

	settled bool
}

type shortWindow string

const (
	windowSecond shortWindow = "second"
	windowMinute shortWindow = "minute"
	windowHour   shortWindow = "hour"
)

type longWindow struct {
	period string
	key    string
}

// TryReserve claims one unit of a channel's allowance.
//
// It reports false when the channel has no capacity right now, with a reason
// for the audit trail. The caller is expected to put the delivery back in the
// queue rather than treat it as a failure — waiting is the correct response to
// an allowance that has been spent.
//
// A channel that declares no limits returns a reservation that costs nothing
// to settle, so the call sites do not need a special case.
func (l *Limiter) TryReserve(ctx context.Context, channelName string, limits Limits) (*Reservation, bool, string) {
	res := &Reservation{limiter: l, channel: channelName, limits: limits}

	if limits.IsZero() {
		return res, true, ""
	}

	l.mu.Lock()
	now := l.now()
	res.now = now
	windows := l.windowsFor(channelName)

	// The short windows are checked together and only recorded once every one
	// of them has room. Recording as we go would leave the earlier windows
	// charged for a reservation that was then refused.
	pending := make([]shortWindow, 0, 3)
	for _, w := range []struct {
		name   shortWindow
		limit  int
		window time.Duration
	}{
		{windowSecond, limits.PerSecond, time.Second},
		{windowMinute, limits.PerMinute, time.Minute},
		{windowHour, limits.PerHour, time.Hour},
	} {
		if w.limit <= 0 {
			continue
		}
		if windows.count(w.name, now, w.window) >= w.limit {
			l.mu.Unlock()
			return nil, false, fmt.Sprintf("channel %q has used its %s allowance of %d",
				channelName, w.name, w.limit)
		}
		pending = append(pending, w.name)
	}

	for _, name := range pending {
		windows.add(name, now)
	}
	res.short = pending
	l.mu.Unlock()

	// The long windows go to the store, which is where the atomic
	// check-and-increment lives. Without a store they are not enforced at
	// all, which is right for tests and wrong in production.
	for _, w := range []struct {
		kind  string
		limit int
	}{
		{"day", limits.PerDay},
		{"month", limits.PerMonth},
	} {
		if w.limit <= 0 {
			continue
		}
		if l.counters == nil {
			continue
		}

		key := periodKey(w.kind, now)

		ok, err := l.counters.TryConsumeCounter(ctx, channelName, key, w.limit, now)
		if err != nil {
			// Failing open is deliberate: a store that cannot be read should
			// not stop notifications. Failing closed would turn a database
			// blip into a silent outage of every channel at once.
			continue
		}
		if !ok {
			l.releaseShort(res)
			return nil, false, fmt.Sprintf("channel %q has used its %s allowance of %d",
				channelName, w.kind, w.limit)
		}
		res.long = append(res.long, longWindow{period: w.kind, key: key})
	}

	return res, true, ""
}

// Commit confirms the reservation. The call reached the peer, so the allowance
// is spent.
func (r *Reservation) Commit(ctx context.Context) {
	r.settle(ctx, false)
}

// Release gives the reservation back. The call never reached the peer, so
// there is nothing to charge for.
func (r *Reservation) Release(ctx context.Context) {
	r.settle(ctx, true)
}

func (r *Reservation) settle(ctx context.Context, release bool) {
	if r == nil || r.settled {
		return
	}
	r.settled = true

	if release {
		r.limiter.releaseShort(r)
		for _, w := range r.long {
			if err := r.limiter.counters.ReleaseCounter(ctx, r.channel, w.key); err != nil {
				// Nothing useful to do here; the count corrects itself when
				// the window rolls over.
				continue
			}
		}
	}
}

func (l *Limiter) releaseShort(r *Reservation) {
	if len(r.short) == 0 {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	windows := l.windowsFor(r.channel)
	for _, name := range r.short {
		windows.remove(name)
	}
}

func (l *Limiter) windowsFor(channelName string) *channelWindows {
	w, ok := l.windows[channelName]
	if !ok {
		w = newChannelWindows()
		l.windows[channelName] = w
	}
	return w
}

// periodKey is the storage key for a long window.
//
// Day and month use fixed periods, not sliding ones: a platform's daily
// allowance resets at midnight, not "24 hours after each call". A sliding
// window would let a caller spend a full day's allowance and then another one
// an hour later, which is not what the limit means.
func periodKey(kind string, now time.Time) string {
	switch kind {
	case "month":
		return now.Format("2006-01")
	default:
		return now.Format("2006-01-02")
	}
}

// ------------------------------------------------------------ short windows

// channelWindows holds the in-memory sliding windows.
//
// A slice of timestamps rather than a counter: a counter would have to be
// decremented when an entry ages out, and there is nothing to hang that on.
type channelWindows struct {
	mu      sync.Mutex
	entries map[shortWindow][]time.Time
}

func newChannelWindows() *channelWindows {
	return &channelWindows{entries: map[shortWindow][]time.Time{}}
}

func (w *channelWindows) count(name shortWindow, now time.Time, window time.Duration) int {
	w.mu.Lock()
	defer w.mu.Unlock()

	cutoff := now.Add(-window)
	kept := w.entries[name][:0]
	for _, t := range w.entries[name] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	w.entries[name] = kept

	return len(kept)
}

func (w *channelWindows) add(name shortWindow, now time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.entries[name] = append(w.entries[name], now)
}

// remove drops the oldest entry, which is the one this reservation added.
func (w *channelWindows) remove(name shortWindow) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if len(w.entries[name]) > 0 {
		w.entries[name] = w.entries[name][1:]
	}
}
