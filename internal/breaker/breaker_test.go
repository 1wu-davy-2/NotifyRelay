package breaker

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"notifyrelay/internal/channel"
	"notifyrelay/internal/store"
	"notifyrelay/internal/store/sqlite"
)

func newStore(t *testing.T) store.Store {
	t.Helper()

	s, err := sqlite.Open(filepath.Join(t.TempDir(), "breaker.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func testSettings() Settings {
	return Settings{
		FailureThreshold: 3,
		SuccessThreshold: 2,
		OpenTimeout:      time.Minute,
		HalfOpenProbes:   1,
	}
}

// fail records a transient failure, which is what a channel fault looks like.
func fail(b *Breaker, ctx context.Context, now time.Time) {
	b.Record(ctx, channel.ClassTransient, now)
}

func TestBreaker_OpensAfterConsecutiveFailures(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()

	b := NewManager(testSettings(), nil, nil).For("oncall")

	for i := 0; i < 2; i++ {
		fail(b, ctx, now)
		if ok, _ := b.Allow(ctx, now); !ok {
			t.Fatalf("the breaker opened after %d failures, want %d", i+1, testSettings().FailureThreshold)
		}
	}

	fail(b, ctx, now)

	if ok, why := b.Allow(ctx, now); ok {
		t.Error("the breaker should be open after three consecutive failures")
	} else if why == "" {
		t.Error("a refusal should explain itself for the audit trail")
	}
}

// The failure that motivates this rule: one bad recipient address must not take
// the whole channel out of service. The endpoint answered, and it answered
// correctly — the message was the problem.
func TestBreaker_PermanentRejectionsAreNotChannelFaults(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()

	b := NewManager(testSettings(), nil, nil).For("oncall")

	for i := 0; i < 20; i++ {
		b.Record(ctx, channel.ClassPermanent, now)
	}

	if ok, _ := b.Allow(ctx, now); !ok {
		t.Fatal("a run of permanent rejections opened the breaker; the channel is healthy")
	}
}

func TestBreaker_SuccessResetsTheFailureCount(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()

	b := NewManager(testSettings(), nil, nil).For("oncall")

	fail(b, ctx, now)
	fail(b, ctx, now)
	b.Record(ctx, channel.ClassSent, now) // back to zero
	fail(b, ctx, now)
	fail(b, ctx, now)

	if ok, _ := b.Allow(ctx, now); !ok {
		t.Error("a success should reset the consecutive failure count")
	}
}

func TestBreaker_OpensThenProbesAfterTheTimeout(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	settings := testSettings()

	b := NewManager(settings, nil, nil).For("oncall")

	for i := 0; i < settings.FailureThreshold; i++ {
		fail(b, ctx, now)
	}
	if ok, _ := b.Allow(ctx, now); ok {
		t.Fatal("the breaker should be open")
	}

	// Still inside the open timeout.
	if ok, _ := b.Allow(ctx, now.Add(settings.OpenTimeout/2)); ok {
		t.Error("the breaker admitted a probe before the open timeout elapsed")
	}

	// Past it: the next call becomes a probe.
	if ok, _ := b.Allow(ctx, now.Add(settings.OpenTimeout+time.Second)); !ok {
		t.Error("the breaker should admit a probe once the open timeout has elapsed")
	}
}

// A channel that has just come back should be tested, not handed the backlog.
// If it were still unwell, the full load would knock it over again.
func TestBreaker_HalfOpenAdmitsOnlyTheConfiguredProbes(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	settings := testSettings()
	settings.HalfOpenProbes = 2

	b := NewManager(settings, nil, nil).For("oncall")

	for i := 0; i < settings.FailureThreshold; i++ {
		fail(b, ctx, now)
	}

	probeAt := now.Add(settings.OpenTimeout + time.Second)

	if ok, _ := b.Allow(ctx, probeAt); !ok {
		t.Fatal("the first probe should be admitted")
	}

	// One probe is in flight; one more slot is available.
	if ok, _ := b.Allow(ctx, probeAt); !ok {
		t.Error("the second probe should be admitted")
	}

	// Both slots are taken; the backlog must wait.
	if ok, why := b.Allow(ctx, probeAt); ok {
		t.Error("a third concurrent probe was admitted; half-open must be limited")
	} else if why == "" {
		t.Error("a refusal should explain itself")
	}

	// One probe succeeds, freeing a slot.
	b.Record(ctx, channel.ClassSent, probeAt)
	if ok, _ := b.Allow(ctx, probeAt); !ok {
		t.Error("a completed probe should free its slot")
	}
}

func TestBreaker_ClosesAfterEnoughProbeSuccesses(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	settings := testSettings()
	settings.HalfOpenProbes = 1
	settings.SuccessThreshold = 2

	b := NewManager(settings, nil, nil).For("oncall")

	for i := 0; i < settings.FailureThreshold; i++ {
		fail(b, ctx, now)
	}

	probeAt := now.Add(settings.OpenTimeout + time.Second)

	for i := 0; i < settings.SuccessThreshold; i++ {
		if ok, _ := b.Allow(ctx, probeAt); !ok {
			t.Fatalf("probe %d was not admitted", i+1)
		}
		b.Record(ctx, channel.ClassSent, probeAt)
	}

	state, failures := b.Snapshot(ctx, probeAt)
	if state != StateClosed {
		t.Errorf("state = %s after %d successful probes, want closed", state, settings.SuccessThreshold)
	}
	if failures != 0 {
		t.Errorf("failures = %d after recovery, want 0", failures)
	}

	// And the backlog flows again.
	for i := 0; i < 10; i++ {
		if ok, _ := b.Allow(ctx, probeAt); !ok {
			t.Fatal("a closed breaker should admit everything")
		}
	}
}

func TestBreaker_AFailedProbeReopens(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	settings := testSettings()

	b := NewManager(settings, nil, nil).For("oncall")

	for i := 0; i < settings.FailureThreshold; i++ {
		fail(b, ctx, now)
	}

	probeAt := now.Add(settings.OpenTimeout + time.Second)
	if ok, _ := b.Allow(ctx, probeAt); !ok {
		t.Fatal("the probe should be admitted")
	}

	b.Record(ctx, channel.ClassTransient, probeAt)

	if ok, _ := b.Allow(ctx, probeAt); ok {
		t.Error("a failed probe should put the breaker straight back to open")
	}

	// And the cycle starts again: the open timeout is measured from the
	// failed probe, not from the original failure.
	if ok, _ := b.Allow(ctx, probeAt.Add(settings.OpenTimeout + time.Second)); !ok {
		t.Error("the breaker should probe again after the new open timeout")
	}
}

// The acceptance criterion: a restart must not forget an outage. The process
// coming back is frequently part of the incident, and a breaker that reset
// would send the whole backlog into an endpoint that is still down.
func TestBreaker_StateSurvivesARestart(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	settings := testSettings()

	persistence := newStore(t)

	first := NewManager(settings, persistence, nil).For("oncall")
	for i := 0; i < settings.FailureThreshold; i++ {
		fail(first, ctx, now)
	}
	if ok, _ := first.Allow(ctx, now); ok {
		t.Fatal("the breaker should be open before the restart")
	}

	// A fresh manager, as a restarted process would build.
	second := NewManager(settings, persistence, nil).For("oncall")

	state, _ := second.Snapshot(ctx, now)
	if state != StateOpen {
		t.Fatalf("state after restart = %s, want open", state)
	}
	if ok, _ := second.Allow(ctx, now); ok {
		t.Error("a restarted process admitted a delivery to a channel that was open")
	}
}

// A breaker that was closed before the restart must not come back open.
func TestBreaker_ClosedStateSurvivesARestart(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()

	persistence := newStore(t)

	first := NewManager(testSettings(), persistence, nil).For("oncall")
	fail(first, ctx, now)
	fail(first, ctx, now)
	// Two failures, below the threshold.
	if ok, _ := first.Allow(ctx, now); !ok {
		t.Fatal("the breaker should still be closed")
	}

	second := NewManager(testSettings(), persistence, nil).For("oncall")
	if ok, _ := second.Allow(ctx, now); !ok {
		t.Error("a restarted process opened a breaker that was closed")
	}
}

func TestBreaker_ConnectErrorCounts(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	settings := testSettings()

	b := NewManager(settings, nil, nil).For("oncall")

	for i := 0; i < settings.FailureThreshold; i++ {
		b.Record(ctx, channel.ClassConnectError, now)
	}

	if ok, _ := b.Allow(ctx, now); ok {
		t.Error("connect errors are channel faults and should open the breaker")
	}
}

// ------------------------------------------------------------------- Abandon

// A probe slot counts a probe that is in flight. One that will never be in
// flight has to give its slot back, or the breaker admits nothing ever again.
func TestBreaker_AbandonReturnsTheSlot(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	settings := testSettings()
	settings.HalfOpenProbes = 1

	b := NewManager(settings, nil, nil).For("oncall")
	for i := 0; i < settings.FailureThreshold; i++ {
		fail(b, ctx, now)
	}

	probeAt := now.Add(settings.OpenTimeout + time.Second)

	if ok, _ := b.Allow(ctx, probeAt); !ok {
		t.Fatal("the probe should be admitted")
	}
	if ok, _ := b.Allow(ctx, probeAt); ok {
		t.Fatal("a second probe was admitted; the limit is one")
	}

	// The delivery was admitted and then never reached the channel.
	b.Abandon(ctx, probeAt)

	if ok, why := b.Allow(ctx, probeAt); !ok {
		t.Errorf("the slot was not returned: %s", why)
	}
}

// Abandon must free only the caller's own slot. Freeing one that another probe
// still holds would let a half-open breaker admit more than it promised, which
// is the load the limit exists to prevent.
func TestBreaker_AbandonDoesNotFreeAnotherProbesSlot(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	settings := testSettings()
	settings.HalfOpenProbes = 2

	b := NewManager(settings, nil, nil).For("oncall")
	for i := 0; i < settings.FailureThreshold; i++ {
		fail(b, ctx, now)
	}

	probeAt := now.Add(settings.OpenTimeout + time.Second)

	if ok, _ := b.Allow(ctx, probeAt); !ok {
		t.Fatal("the first probe should be admitted")
	}
	if ok, _ := b.Allow(ctx, probeAt); !ok {
		t.Fatal("the second probe should be admitted")
	}

	// One of them is abandoned; the other is still in flight.
	b.Abandon(ctx, probeAt)

	if ok, _ := b.Allow(ctx, probeAt); !ok {
		t.Error("the abandoned slot was not returned")
	}
	if ok, why := b.Allow(ctx, probeAt); ok {
		t.Error("a third probe was admitted; only one slot was given back")
	} else if why == "" {
		t.Error("a refusal should explain itself")
	}
}

// Abandon is a no-op outside half-open: the slot is a half-open concept, and
// decrementing a counter that is not counting anything would corrupt the count
// the next half-open period relies on.
func TestBreaker_AbandonIsANoOpWhenClosed(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()

	b := NewManager(testSettings(), nil, nil).For("oncall")

	b.Abandon(ctx, now)

	if state, failures := b.Snapshot(ctx, now); state != StateClosed || failures != 0 {
		t.Errorf("state = %s failures = %d after Abandon on a closed breaker", state, failures)
	}
	for i := 0; i < 10; i++ {
		if ok, _ := b.Allow(ctx, now); !ok {
			t.Fatal("a closed breaker should admit everything")
		}
	}
}

// The corollary of reopening on the first failed probe: a sibling probe that was
// already in flight and comes back successful must not resurrect the breaker.
// Its success is older news than the failure that reopened it.
func TestBreaker_ALateProbeSuccessCannotReclose(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	settings := testSettings()
	settings.HalfOpenProbes = 2
	settings.SuccessThreshold = 1

	b := NewManager(settings, nil, nil).For("oncall")
	for i := 0; i < settings.FailureThreshold; i++ {
		fail(b, ctx, now)
	}

	probeAt := now.Add(settings.OpenTimeout + time.Second)

	// Two probes go out together; both slots are taken.
	if ok, _ := b.Allow(ctx, probeAt); !ok {
		t.Fatal("the first probe should be admitted")
	}
	if ok, _ := b.Allow(ctx, probeAt); !ok {
		t.Fatal("the second probe should be admitted")
	}

	// The first one fails, and the breaker reopens immediately.
	b.Record(ctx, channel.ClassTransient, probeAt)

	// The second one then succeeds. It must not close a breaker that the
	// failure has already reopened.
	b.Record(ctx, channel.ClassSent, probeAt)

	if state, _ := b.Snapshot(ctx, probeAt); state != StateOpen {
		t.Errorf("state = %s after a late probe success, want open", state)
	}
	if ok, _ := b.Allow(ctx, probeAt); ok {
		t.Error("a delivery was admitted; the breaker should still be open")
	}
}
