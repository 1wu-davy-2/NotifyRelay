package token

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGet_CachesTheToken(t *testing.T) {
	var calls int32
	c := New(func(context.Context) (string, time.Duration, error) {
		atomic.AddInt32(&calls, 1)
		return "tok", time.Hour, nil
	})

	for i := 0; i < 5; i++ {
		got, err := c.Get(context.Background())
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got != "tok" {
			t.Fatalf("token = %q", got)
		}
	}

	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("fetched %d times, want 1", n)
	}
}

// The failure this package exists for: twenty goroutines noticing an expired
// token at the same instant and all fetching one. The platform sees twenty
// credential requests and nineteen tokens are discarded.
func TestGet_ConcurrentCallersFetchOnce(t *testing.T) {
	var calls int32
	c := New(func(context.Context) (string, time.Duration, error) {
		atomic.AddInt32(&calls, 1)
		time.Sleep(20 * time.Millisecond) // widen the race window
		return "tok", time.Hour, nil
	})

	const goroutines = 20
	var wg sync.WaitGroup
	errs := make([]error, goroutines)

	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = c.Get(context.Background())
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: %v", i, err)
		}
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("%d goroutines caused %d fetches, want 1", goroutines, n)
	}
}

func TestGet_RefreshesAnExpiredToken(t *testing.T) {
	var calls int32
	c := New(func(context.Context) (string, time.Duration, error) {
		n := atomic.AddInt32(&calls, 1)
		// The first token is already past the refresh margin.
		if n == 1 {
			return "first", time.Second, nil
		}
		return "second", time.Hour, nil
	})
	c.SetRefreshMargin(time.Minute)

	if got, _ := c.Get(context.Background()); got != "first" {
		t.Fatalf("token = %q, want first", got)
	}
	if got, _ := c.Get(context.Background()); got != "second" {
		t.Errorf("token = %q, want second (a token inside the refresh margin is stale)", got)
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Errorf("fetched %d times, want 2", n)
	}
}

// A transient network failure should not take notifications down while a
// perfectly usable token is still in hand.
func TestGet_ServesTheOldTokenWhenRefreshFails(t *testing.T) {
	var calls int32
	c := New(func(context.Context) (string, time.Duration, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			// Inside the refresh margin but not yet expired.
			return "still-good", 30 * time.Second, nil
		}
		return "", 0, errors.New("network is down")
	})
	c.SetRefreshMargin(time.Minute)

	if got, _ := c.Get(context.Background()); got != "still-good" {
		t.Fatalf("token = %q", got)
	}

	got, err := c.Get(context.Background())
	if err != nil {
		t.Fatalf("a failed refresh must not fail the caller while the token is unexpired: %v", err)
	}
	if got != "still-good" {
		t.Errorf("token = %q, want the previous one", got)
	}
}

func TestGet_ReportsAFetchFailureWhenThereIsNoUsableToken(t *testing.T) {
	c := New(func(context.Context) (string, time.Duration, error) {
		return "", 0, errors.New("credentials rejected")
	})

	if _, err := c.Get(context.Background()); err == nil {
		t.Fatal("expected an error when no token has ever been obtained")
	}
}

func TestInvalidate_ForcesARefetch(t *testing.T) {
	var calls int32
	c := New(func(context.Context) (string, time.Duration, error) {
		n := atomic.AddInt32(&calls, 1)
		return "tok" + string(rune('0'+n)), time.Hour, nil
	})

	first, _ := c.Get(context.Background())
	c.Invalidate()
	second, _ := c.Get(context.Background())

	if first == second {
		t.Errorf("Invalidate did not force a refetch (both %q)", first)
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Errorf("fetched %d times, want 2", n)
	}
}

func TestGet_RespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	c := New(func(ctx context.Context) (string, time.Duration, error) {
		return "", 0, ctx.Err()
	})

	if _, err := c.Get(ctx); err == nil {
		t.Error("a cancelled context should surface as an error")
	}
}
