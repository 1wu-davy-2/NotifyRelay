package quota

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"notifyrelay/internal/store/sqlite"
)

func newLimiter(t *testing.T) (*Limiter, func(time.Time)) {
	t.Helper()

	s, err := sqlite.Open(filepath.Join(t.TempDir(), "quota.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	l := New(s)

	var mu sync.Mutex
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)

	l.SetClock(func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	})

	return l, func(at time.Time) {
		mu.Lock()
		defer mu.Unlock()
		now = at
	}
}

func TestReserve_UnlimitedCostsNothing(t *testing.T) {
	l, _ := newLimiter(t)
	ctx := context.Background()

	// A channel with no declared limits must not need a special case at the
	// call site, so the reservation has to be usable even though nothing was
	// counted.
	for i := 0; i < 100; i++ {
		res, ok, why := l.TryReserve(ctx, "oncall", Limits{})
		if !ok {
			t.Fatalf("an unlimited channel refused: %s", why)
		}
		res.Commit(ctx)
	}
}

func TestReserve_EnforcesTheSecondWindow(t *testing.T) {
	l, _ := newLimiter(t)
	ctx := context.Background()
	limits := Limits{PerSecond: 3}

	for i := 0; i < 3; i++ {
		res, ok, _ := l.TryReserve(ctx, "oncall", limits)
		if !ok {
			t.Fatalf("reservation %d was refused inside the allowance", i+1)
		}
		res.Commit(ctx)
	}

	if _, ok, why := l.TryReserve(ctx, "oncall", limits); ok {
		t.Error("a fourth reservation was admitted into a three-per-second allowance")
	} else if why == "" {
		t.Error("a refusal should explain itself for the audit trail")
	}
}

// A sliding window, not a fixed bucket: the allowance comes back as the old
// calls age out, not all at once at a period boundary.
func TestReserve_SlidingWindowAgesOut(t *testing.T) {
	l, setClock := newLimiter(t)
	ctx := context.Background()
	limits := Limits{PerSecond: 2}

	start := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	setClock(start)

	res, ok, _ := l.TryReserve(ctx, "oncall", limits)
	if !ok {
		t.Fatal("the first reservation should fit")
	}
	res.Commit(ctx)

	// A second call half a second later fills the window.
	setClock(start.Add(500 * time.Millisecond))
	res, ok, _ = l.TryReserve(ctx, "oncall", limits)
	if !ok {
		t.Fatal("the second reservation should fit")
	}
	res.Commit(ctx)

	if _, ok, _ := l.TryReserve(ctx, "oncall", limits); ok {
		t.Fatal("the allowance should be spent")
	}

	// At 1.1s the first call is more than a second old and has aged out; the
	// second is only 0.6s old and has not. A fixed bucket would return both at
	// once, or neither.
	setClock(start.Add(1100 * time.Millisecond))
	if _, ok, _ := l.TryReserve(ctx, "oncall", limits); !ok {
		t.Error("a call from more than a second ago should have aged out")
	}
}

// The point of the reservation: a call that never reached the peer is not
// charged for. Without this, an unreachable channel would spend its whole
// allowance on messages nobody received and then be refused for the rest of
// the day.
func TestRelease_ReturnsTheAllowance(t *testing.T) {
	l, _ := newLimiter(t)
	ctx := context.Background()
	limits := Limits{PerSecond: 1}

	res, ok, _ := l.TryReserve(ctx, "oncall", limits)
	if !ok {
		t.Fatal("the first reservation should succeed")
	}
	if _, ok, _ := l.TryReserve(ctx, "oncall", limits); ok {
		t.Fatal("the allowance should be spent")
	}

	res.Release(ctx)

	if _, ok, _ := l.TryReserve(ctx, "oncall", limits); !ok {
		t.Error("released capacity was not returned")
	}
}

func TestCommit_KeepsTheAllowanceSpent(t *testing.T) {
	l, _ := newLimiter(t)
	ctx := context.Background()
	limits := Limits{PerSecond: 1}

	res, _, _ := l.TryReserve(ctx, "oncall", limits)
	res.Commit(ctx)

	if _, ok, _ := l.TryReserve(ctx, "oncall", limits); ok {
		t.Error("a committed reservation should stay spent")
	}
}

func TestRelease_IsIdempotent(t *testing.T) {
	l, _ := newLimiter(t)
	ctx := context.Background()
	limits := Limits{PerSecond: 2}

	res, _, _ := l.TryReserve(ctx, "oncall", limits)

	// Settling twice must not hand back two slots for one reservation.
	res.Release(ctx)
	res.Release(ctx)

	if _, ok, _ := l.TryReserve(ctx, "oncall", limits); !ok {
		t.Fatal("the first replacement should fit")
	}
	if _, ok, _ := l.TryReserve(ctx, "oncall", limits); !ok {
		t.Fatal("the second replacement should fit")
	}
	if _, ok, _ := l.TryReserve(ctx, "oncall", limits); ok {
		t.Error("the allowance grew past its limit; a double release returned too much")
	}
}

// Day and month use fixed periods, because that is what a platform's allowance
// means: it resets at midnight, not 24 hours after each call.
func TestReserve_PersistsTheDailyAllowance(t *testing.T) {
	l, setClock := newLimiter(t)
	ctx := context.Background()
	limits := Limits{PerDay: 3}

	start := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	setClock(start)

	for i := 0; i < 3; i++ {
		res, ok, why := l.TryReserve(ctx, "oncall", limits)
		if !ok {
			t.Fatalf("reservation %d refused: %s", i+1, why)
		}
		res.Commit(ctx)
	}

	if _, ok, why := l.TryReserve(ctx, "oncall", limits); ok {
		t.Error("a fourth reservation was admitted into a three-per-day allowance")
	} else if why == "" {
		t.Error("a refusal should explain itself")
	}

	// Still the same day: the allowance stays spent even after the short
	// windows have long since emptied.
	setClock(start.Add(6 * time.Hour))
	if _, ok, _ := l.TryReserve(ctx, "oncall", limits); ok {
		t.Error("the daily allowance reset before the day ended")
	}

	// The next day it is available again.
	setClock(start.Add(24 * time.Hour))
	if _, ok, why := l.TryReserve(ctx, "oncall", limits); !ok {
		t.Errorf("the daily allowance did not reset on the new day: %s", why)
	}
}

// A reservation that is refused by a later window must not leave the earlier
// ones charged, or a refused call would still cost allowance.
func TestReserve_RollsBackShortWindowsWhenALongWindowRefuses(t *testing.T) {
	l, _ := newLimiter(t)
	ctx := context.Background()

	// Fill the daily allowance.
	if _, ok, _ := l.TryReserve(ctx, "oncall", Limits{PerDay: 1}); !ok {
		t.Fatal("the first daily reservation should succeed")
	}

	// Now ask for one that also counts against the second window. The daily
	// window refuses, so the second window must be given back.
	if _, ok, _ := l.TryReserve(ctx, "oncall", Limits{PerSecond: 5, PerDay: 1}); ok {
		t.Fatal("the daily allowance is spent; this should be refused")
	}

	// The per-second allowance must still be whole. Charging it for a
	// reservation that was refused would mean a caller whose daily budget ran
	// out also loses the ability to send the calls it is still allowed.
	for i := 0; i < 5; i++ {
		res, ok, why := l.TryReserve(ctx, "oncall", Limits{PerSecond: 5})
		if !ok {
			t.Fatalf("reservation %d refused after a rolled-back attempt: %s", i+1, why)
		}
		res.Commit(ctx)
	}
}

func TestReserve_ChannelsAreIndependent(t *testing.T) {
	l, _ := newLimiter(t)
	ctx := context.Background()
	limits := Limits{PerSecond: 1}

	if _, ok, _ := l.TryReserve(ctx, "channel-a", limits); !ok {
		t.Fatal("channel-a should have room")
	}
	if _, ok, _ := l.TryReserve(ctx, "channel-b", limits); !ok {
		t.Error("one channel's spending should not consume another's allowance")
	}
}

func TestReserve_PersistedCounterSurvivesANewLimiter(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "quota.db")

	s, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	first := New(s)
	first.SetClock(func() time.Time { return now })

	res, ok, _ := first.TryReserve(ctx, "oncall", Limits{PerDay: 2})
	if !ok {
		t.Fatal("the first reservation should succeed")
	}
	res.Commit(ctx)
	s.Close()

	// A restarted process must not hand back a day's allowance that has
	// already been spent.
	second, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer second.Close()

	l := New(second)
	l.SetClock(func() time.Time { return now })

	if _, ok, _ := l.TryReserve(ctx, "oncall", Limits{PerDay: 2}); !ok {
		t.Fatal("the second of two daily reservations should fit")
	}
	if _, ok, why := l.TryReserve(ctx, "oncall", Limits{PerDay: 2}); ok {
		t.Error("the daily allowance was forgotten across a restart")
	} else if why == "" {
		t.Error("a refusal should explain itself")
	}
}
