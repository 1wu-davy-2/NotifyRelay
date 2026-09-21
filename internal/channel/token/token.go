// Package token caches short-lived API credentials.
//
// WeCom and DingTalk applications authenticate with a token that lives for a
// couple of hours. Fetching one per message doubles the request count and
// walks straight into the platform's own rate limit; fetching one per process
// at startup means every process dies at the same moment two hours later.
//
// The third failure mode is the one this package exists for: twenty goroutines
// noticing the token has expired at the same instant and all fetching a new
// one. The platform sees twenty credential requests, and nineteen of the
// twenty tokens are thrown away.
package token

import (
	"context"
	"sync"
	"time"
)

// Fetcher obtains a fresh token from the platform.
type Fetcher func(ctx context.Context) (value string, ttl time.Duration, err error)

// Cache holds one token and refreshes it on demand.
//
// The zero value is ready to use.
type Cache struct {
	// refreshMargin is how long before expiry a token is considered stale, so
	// a request that starts just before expiry does not carry a token that
	// expires mid-flight.
	refreshMargin time.Duration

	mu     sync.Mutex
	value  string
	expiry time.Time
	fetch  Fetcher
}

// New returns a cache backed by the given fetcher.
func New(fetch Fetcher) *Cache {
	return &Cache{fetch: fetch, refreshMargin: 5 * time.Minute}
}

// SetRefreshMargin overrides the default five-minute margin.
func (c *Cache) SetRefreshMargin(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refreshMargin = d
}

// Get returns a valid token, fetching one if necessary.
//
// The mutex is held across the fetch. That serialises concurrent callers, so
// exactly one request reaches the platform and the rest find a fresh token
// waiting — which is the whole point. It also means a slow fetch blocks the
// others, but they would have waited for the same token regardless.
func (c *Cache) Get(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.valid() {
		return c.value, nil
	}

	value, ttl, err := c.fetch(ctx)
	if err != nil {
		// Keep serving the previous token if it has not actually expired yet:
		// a transient network failure should not take notifications down.
		if c.value != "" && time.Now().Before(c.expiry) {
			return c.value, nil
		}
		return "", err
	}

	c.value = value
	c.expiry = time.Now().Add(ttl)
	return c.value, nil
}

// Invalidate discards the cached token, forcing the next Get to fetch one.
// Use it when the platform rejects a token that looked valid.
func (c *Cache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.value = ""
	c.expiry = time.Time{}
}

func (c *Cache) valid() bool {
	if c.value == "" {
		return false
	}
	return time.Now().Add(c.refreshMargin).Before(c.expiry)
}
