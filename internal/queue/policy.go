// Package queue delivers queued notifications and retries the ones worth
// retrying.
package queue

import "time"

// Policy decides when a failed delivery is tried again, and when to stop.
type Policy struct {
	// MaxAttempts bounds how many times a delivery is tried.
	MaxAttempts int
	// Backoff is the delay before each retry. The last entry repeats once the
	// list runs out.
	Backoff []time.Duration
	// MaxAge bounds how long a delivery may stay in the queue at all.
	//
	// Without it, a message that is only ever released — because its channel
	// is permanently unreachable — would be retried forever without ever
	// spending an attempt.
	MaxAge time.Duration
}

// DefaultPolicy is the cadence the plan settled on.
//
// Fixed increasing intervals rather than exponential backoff: mail and IM
// failures are dominated by greylisting and temporary 4xx, where the first
// few exponential steps (30s, 60s, 120s) are far too dense to let a transient
// condition clear, and the later ones far too sparse to keep a notification
// timely.
func DefaultPolicy() Policy {
	return Policy{
		MaxAttempts: 5,
		Backoff: []time.Duration{
			30 * time.Second,
			2 * time.Minute,
			10 * time.Minute,
			time.Hour,
			4 * time.Hour,
		},
		MaxAge: 24 * time.Hour,
	}
}

// Normalize fills in defaults for anything left unset.
func (p Policy) Normalize() Policy {
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = DefaultPolicy().MaxAttempts
	}
	if len(p.Backoff) == 0 {
		p.Backoff = DefaultPolicy().Backoff
	}
	if p.MaxAge <= 0 {
		p.MaxAge = DefaultPolicy().MaxAge
	}
	return p
}

// Retry reports when to try again, and whether to try at all.
//
// attempts is how many attempts have already been made, age how long the
// delivery has been in the queue, now the current time.
func (p Policy) Retry(attempts int, age time.Duration, now time.Time) (time.Time, bool) {
	p = p.Normalize()

	if attempts >= p.MaxAttempts {
		return time.Time{}, false
	}
	if p.MaxAge > 0 && age >= p.MaxAge {
		return time.Time{}, false
	}

	return now.Add(p.Delay(attempts)), true
}

// Delay is how long to wait before the attempt after the given count.
func (p Policy) Delay(attempts int) time.Duration {
	p = p.Normalize()

	if attempts < 0 {
		attempts = 0
	}
	if attempts >= len(p.Backoff) {
		attempts = len(p.Backoff) - 1
	}
	return p.Backoff[attempts]
}

// ExhaustedReason says why a delivery is being dead-lettered, or "" when it
// still has attempts left.
func (p Policy) ExhaustedReason(attempts int, age time.Duration) string {
	p = p.Normalize()

	switch {
	case attempts >= p.MaxAttempts:
		return "attempts exhausted"
	case p.MaxAge > 0 && age >= p.MaxAge:
		return "expired before it could be delivered"
	default:
		return ""
	}
}
