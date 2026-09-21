package router

import (
	"context"
	"sync"
	"time"
)

// limiter throttles deliveries per channel instance.
//
// A channel's declared rate is a ceiling its upstream enforces. Being briefly
// slower than necessary costs latency; being briefly faster costs a rejected
// message, and for a notification relay a message that arrives late is worth
// more than one that is dropped. So this spaces deliveries evenly rather than
// allowing a burst.
//
// Rate limiting lives in the core rather than in each channel so that a
// channel author cannot forget it, and so the wait is charged against the
// caller's context.
type limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}

func newLimiter() *limiter {
	return &limiter{buckets: make(map[string]*bucket)}
}

// wait blocks until the channel may accept another message.
//
// A channel that declares no rate returns immediately, without taking the lock
// or allocating anything — the common case costs nothing.
func (l *limiter) wait(ctx context.Context, key string, perSec float64) error {
	if perSec <= 0 {
		return nil
	}

	b := l.bucket(key, perSec)

	wait := b.reserve()
	if wait <= 0 {
		return nil
	}

	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		// The slot stays reserved. Handing it back would let a caller that
		// keeps timing out consume the channel's whole allowance.
		return ctx.Err()
	}
}

func (l *limiter) bucket(key string, perSec float64) *bucket {
	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{}
		l.buckets[key] = b
	}
	// Keep an existing bucket in step with the configuration, so a reloaded
	// rate takes effect without discarding the accumulated state.
	b.setInterval(time.Duration(float64(time.Second) / perSec))
	return b
}

// bucket hands out one slot per interval.
//
// reserve() returns how long the caller must wait, having already claimed the
// slot. Claiming before waiting is what keeps concurrent callers in order:
// each takes the next free instant and they queue behind one another instead
// of all deciding at once that the channel is idle.
type bucket struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
}

func (b *bucket) setInterval(d time.Duration) {
	if d <= 0 {
		d = time.Nanosecond
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	b.interval = d
}

func (b *bucket) reserve() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	if b.next.Before(now) {
		// Idle long enough that the schedule has fallen behind; start afresh
		// rather than firing a backlog of slots at the endpoint at once.
		b.next = now
	}

	slot := b.next
	b.next = slot.Add(b.interval)

	if wait := slot.Sub(now); wait > 0 {
		return wait
	}
	return 0
}
