package router

import (
	"context"
	"testing"
	"time"

	"notifyrelay/internal/breaker"
	"notifyrelay/internal/channel"
	"notifyrelay/internal/config"
	"notifyrelay/internal/quota"
)

// gatedRouter builds a router whose one channel fails on demand.
func gatedRouter(t *testing.T, settings breaker.Settings, limits config.QuotaConfig) (*Router, *fakeChannel) {
	t.Helper()
	registerFake()
	registerControlled()

	fake := &fakeChannel{}
	controlledMu.Lock()
	controlledNext = fake
	controlledMu.Unlock()

	r, err := New(Options{
		Channels: []config.ChannelConfig{{
			Name: "gated", Type: "routertestctl", Quota: limits,
		}},
		DeliverTimeout: time.Second,
		Breaker:        breaker.NewManager(settings, nil, nil),
		Quota:          quota.New(nil),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r, fake
}

// The interaction the no-capacity rule depends on: a message held back because
// the channel is down must not also spend the channel's allowance. Otherwise
// an outage would eat the day's quota without a single message going out.
func TestDeliver_SuspendedChannelDoesNotConsumeQuota(t *testing.T) {
	ctx := context.Background()

	settings := breaker.Settings{
		FailureThreshold: 2,
		SuccessThreshold: 1,
		OpenTimeout:      time.Hour, // stay open for the length of the test
		HalfOpenProbes:   1,
	}

	r, fake := gatedRouter(t, settings, config.QuotaConfig{PerSecond: 100})

	// Two transient failures open the breaker.
	fake.fail = channel.ClassTransient
	for i := 0; i < 2; i++ {
		r.Deliver(ctx, "req", ref("gated"), textMessage("b"))
	}

	fake.mu.Lock()
	callsBefore := fake.calls
	fake.mu.Unlock()

	if callsBefore != 2 {
		t.Fatalf("the channel was called %d times, want 2", callsBefore)
	}

	// Now the breaker is open. Twenty deliveries must all be refused without
	// reaching the channel.
	for i := 0; i < 20; i++ {
		res := r.Deliver(ctx, "req", ref("gated"), textMessage("b"))
		if res.Class() != channel.ClassNotAttempted {
			t.Fatalf("delivery %d: class = %v, want NOT_ATTEMPTED so the queue releases it", i, res.Class())
		}
	}

	fake.mu.Lock()
	callsAfter := fake.calls
	fake.mu.Unlock()

	if callsAfter != callsBefore {
		t.Errorf("a suspended channel was called %d more times", callsAfter-callsBefore)
	}

	// And none of those twenty consumed allowance. The first two did: they
	// reached the channel and were answered, so they are charged for. Two of
	// a hundred are spent, and the other ninety-eight must still be there.
	const spentByRealDeliveries = 2

	for i := 0; i < 100-spentByRealDeliveries; i++ {
		res, ok, why := r.quota.TryReserve(ctx, "gated", quota.Limits{PerSecond: 100})
		if !ok {
			t.Fatalf("reservation %d refused: %s — the refused deliveries spent the allowance", i+1, why)
		}
		res.Commit(ctx)
	}

	if _, ok, _ := r.quota.TryReserve(ctx, "gated", quota.Limits{PerSecond: 100}); ok {
		t.Error("more than the allowance was handed out")
	}
}

// A delivery that reaches the peer is charged for; one that never got there is
// not. This is what keeps an unreachable channel from burning its own budget.
func TestDeliver_ConnectErrorsAreNotCharged(t *testing.T) {
	ctx := context.Background()

	settings := breaker.Settings{
		FailureThreshold: 1000, // never open during this test
		SuccessThreshold: 1,
		OpenTimeout:      time.Hour,
		HalfOpenProbes:   1,
	}

	r, fake := gatedRouter(t, settings, config.QuotaConfig{PerSecond: 4})
	fake.fail = channel.ClassConnectError

	// Four attempts, all failing to reach the peer.
	for i := 0; i < 4; i++ {
		r.Deliver(ctx, "req", ref("gated"), textMessage("b"))
	}

	// Every one of them released its reservation, so the allowance is whole.
	for i := 0; i < 4; i++ {
		res, ok, why := r.quota.TryReserve(ctx, "gated", quota.Limits{PerSecond: 4})
		if !ok {
			t.Fatalf("reservation %d refused: %s — a call that never reached the peer was charged for", i+1, why)
		}
		res.Commit(ctx)
	}
}

func TestDeliver_TransientErrorsAreCharged(t *testing.T) {
	ctx := context.Background()

	settings := breaker.Settings{
		FailureThreshold: 1000,
		SuccessThreshold: 1,
		OpenTimeout:      time.Hour,
		HalfOpenProbes:   1,
	}

	r, fake := gatedRouter(t, settings, config.QuotaConfig{PerSecond: 4})
	fake.fail = channel.ClassTransient

	for i := 0; i < 4; i++ {
		r.Deliver(ctx, "req", ref("gated"), textMessage("b"))
	}

	// The peer was called and answered, so the allowance is spent.
	if _, ok, _ := r.quota.TryReserve(ctx, "gated", quota.Limits{PerSecond: 4}); ok {
		t.Error("a channel that answered was not charged for the calls")
	}
}

// A delivery held back by a spent allowance says nothing about the channel's
// health. Feeding those refusals to the breaker would let a busy hour take a
// perfectly good channel out of service — and then the messages that were only
// waiting would have nothing left to wait for.
func TestDeliver_QuotaRefusalsDoNotOpenTheBreaker(t *testing.T) {
	ctx := context.Background()

	settings := breaker.Settings{
		FailureThreshold: 3,
		SuccessThreshold: 1,
		OpenTimeout:      time.Hour,
		HalfOpenProbes:   1,
	}

	// Two per second, so the allowance is spent at once and the rest of the
	// test is about what happens to the refusals.
	r, fake := gatedRouter(t, settings, config.QuotaConfig{PerSecond: 2})

	for i := 0; i < 2; i++ {
		if res := r.Deliver(ctx, "req", ref("gated"), textMessage("b")); res.Class() != channel.ClassSent {
			t.Fatalf("delivery %d inside the allowance: class = %v", i+1, res.Class())
		}
	}

	// Ten more, well past the breaker's threshold of three.
	for i := 0; i < 10; i++ {
		res := r.Deliver(ctx, "req", ref("gated"), textMessage("b"))
		if res.Class() != channel.ClassNotAttempted {
			t.Fatalf("refusal %d: class = %v, want NOT_ATTEMPTED so the queue waits", i+1, res.Class())
		}
		if res.Error == "channel is not accepting deliveries" {
			t.Fatal("quota refusals opened the breaker; the channel is healthy")
		}
	}

	fake.mu.Lock()
	calls := fake.calls
	fake.mu.Unlock()

	if calls != 2 {
		t.Errorf("the channel was called %d times, want 2 — quota is checked before the call", calls)
	}
}

// Quota exhaustion is not a failure: it is a "come back later", and the queue
// must treat it that way rather than spending the delivery's retry budget.
func TestDeliver_QuotaExhaustionIsNoCapacity(t *testing.T) {
	ctx := context.Background()

	settings := breaker.Settings{
		FailureThreshold: 1000,
		SuccessThreshold: 1,
		OpenTimeout:      time.Hour,
		HalfOpenProbes:   1,
	}

	r, fake := gatedRouter(t, settings, config.QuotaConfig{PerSecond: 1})

	// The first fits.
	if res := r.Deliver(ctx, "req", ref("gated"), textMessage("b")); res.Class() != channel.ClassSent {
		t.Fatalf("the first delivery: class = %v", res.Class())
	}

	// The allowance is gone; the next is refused as no-capacity, not as a
	// failure.
	res := r.Deliver(ctx, "req", ref("gated"), textMessage("b"))
	if res.Class() != channel.ClassNotAttempted {
		t.Errorf("class = %v, want NOT_ATTEMPTED so the queue waits instead of spending an attempt", res.Class())
	}
	if res.Error == "" {
		t.Error("a refusal should explain itself")
	}

	fake.mu.Lock()
	calls := fake.calls
	fake.mu.Unlock()

	if calls != 1 {
		t.Errorf("the channel was called %d times, want 1 — quota must be checked before the call", calls)
	}
}
