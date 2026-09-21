package router

import (
	"context"
	"testing"
	"time"

	"notifyrelay/internal/breaker"
	"notifyrelay/internal/channel"
	"notifyrelay/internal/config"
)

// A probe slot taken by Allow must be given back even when the channel is never
// called.
//
// Otherwise a single quota refusal during half-open burns the only probe slot,
// and with half_open_probes: 1 the breaker then refuses every delivery forever —
// a channel that recovered long ago stays out of service until a restart. The
// failure is invisible in the class: both a stranded slot and a genuinely open
// breaker report CONNECT_ERROR, which is exactly why skip_reason has to say
// which one it was.
func TestDeliver_BlockedProbeDoesNotStrandTheHalfOpenSlot(t *testing.T) {
	ctx := context.Background()

	settings := breaker.Settings{
		FailureThreshold: 2,
		SuccessThreshold: 1,
		OpenTimeout:      time.Millisecond,
		HalfOpenProbes:   1,
	}

	// One send per minute: the allowance is spent by the first delivery and
	// cannot come back within the test, so every later delivery is refused for
	// quota and only for quota.
	r, fake := gatedRouter(t, settings, config.QuotaConfig{PerMinute: 1})

	// Spend the allowance on a delivery that really reaches the channel.
	if res := r.Deliver(ctx, "req", "gated", textMessage("b")); res.Class() != channel.ClassSent {
		t.Fatalf("the first delivery: class = %v, want SENT", res.Class())
	}

	// Open the breaker. Driven through the breaker rather than through the
	// router because a delivery through the router would now be refused for
	// quota before it ever reached the channel — the allowance is the whole
	// point of the test.
	b := r.breakers.For("gated")
	now := time.Now()
	for i := 0; i < settings.FailureThreshold; i++ {
		b.Record(ctx, channel.ClassTransient, now)
	}

	// Let the open timeout elapse so the next delivery is admitted as a probe.
	time.Sleep(5 * time.Millisecond)

	// The probe is admitted and then blocked by the spent allowance: the
	// channel is never called, so there is no outcome to report.
	res := r.Deliver(ctx, "req", "gated", textMessage("b"))
	if res.Class() != channel.ClassNotAttempted {
		t.Fatalf("the blocked probe: class = %v, want NOT_ATTEMPTED", res.Class())
	}
	if res.Reason() != SkipQuotaExhausted {
		t.Fatalf("the blocked probe: reason = %q, want %q", res.Reason(), SkipQuotaExhausted)
	}

	// The slot must be free again. If it is not, the breaker refuses this
	// delivery as "channel is being probed" — a channel held out of service by
	// a probe that never happened.
	res = r.Deliver(ctx, "req", "gated", textMessage("b"))
	if res.Reason() == SkipBreakerOpen {
		t.Fatal("the half-open probe slot was never returned: the breaker is wedged and will refuse every delivery until a restart")
	}
	if res.Reason() != SkipQuotaExhausted {
		t.Fatalf("reason = %q, want %q — quota, not the breaker, is what refuses these", res.Reason(), SkipQuotaExhausted)
	}

	fake.mu.Lock()
	calls := fake.calls
	fake.mu.Unlock()
	if calls != 1 {
		t.Errorf("the channel was called %d times, want 1", calls)
	}
}

// A delivery the channel actually received reports no skip reason, even when a
// later part of it was held back. The reason means "the channel was not
// called", and a partially delivered message was called.
func TestDeliver_SentDeliveryHasNoSkipReason(t *testing.T) {
	ctx := context.Background()

	r, _ := gatedRouter(t, breaker.Settings{
		FailureThreshold: 1000,
		SuccessThreshold: 1,
		OpenTimeout:      time.Hour,
		HalfOpenProbes:   1,
	}, config.QuotaConfig{})

	res := r.Deliver(ctx, "req", "gated", textMessage("b"))
	if res.WasSkipped() {
		t.Errorf("a delivered message reported skip_reason = %q", res.Reason())
	}
}
