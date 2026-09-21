package admin

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"sync"
	"time"
)

// cookieName is the session cookie.
//
// The __Host- prefix is not used: it requires Secure, which is wrong for the
// plain-HTTP internal deployment this service is built for, and a cookie that
// silently stops being set when TLS is terminated upstream is a worse failure
// than the prefix prevents.
const cookieName = "nr_admin"

// session is one logged-in operator.
type session struct {
	actor   string
	expires time.Time
}

// sessions holds the active logins.
//
// Server-side rather than a signed cookie, for one reason: logout has to
// actually end the session. A signed cookie can only be un-sent by the browser,
// so a copy taken from a shared machine stays valid until it expires, and the
// operator who clicked "log out" has no way to tell.
//
// The cost is that a restart ends every session. For an internal tool with a
// handful of operators that is closer to a feature than a cost, and it is the
// direction the failure should point: nobody is let in who should not be.
type sessions struct {
	mu      sync.Mutex
	entries map[string]session
	ttl     time.Duration

	// now is a seam for tests; production leaves it as time.Now.
	now func() time.Time
}

func newSessions(ttl time.Duration) *sessions {
	return &sessions{
		entries: map[string]session{},
		ttl:     ttl,
		now:     func() time.Time { return time.Now().UTC() },
	}
}

// create starts a session and returns its identifier.
func (s *sessions) create(actor string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("admin: session id: %w", err)
	}
	id := base64.RawURLEncoding.EncodeToString(raw)

	s.mu.Lock()
	defer s.mu.Unlock()

	s.sweepLocked()
	s.entries[id] = session{actor: actor, expires: s.now().Add(s.ttl)}
	return id, nil
}

// lookup returns the actor for a session identifier, if it is still valid.
func (s *sessions) lookup(id string) (string, bool) {
	if id == "" {
		return "", false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.entries[id]
	if !ok {
		return "", false
	}
	if !s.now().Before(entry.expires) {
		delete(s.entries, id)
		return "", false
	}
	return entry.actor, true
}

// end removes a session. Ending one that is already gone is not an error.
func (s *sessions) end(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, id)
}

// count reports how many sessions are live, for tests.
func (s *sessions) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
	return len(s.entries)
}

// sweepLocked drops expired sessions. Called on the way in rather than from a
// goroutine: logins are rare, and a background ticker would be one more thing
// to shut down cleanly for no measurable gain.
func (s *sessions) sweepLocked() {
	now := s.now()
	for id, entry := range s.entries {
		if !now.Before(entry.expires) {
			delete(s.entries, id)
		}
	}
}

// ---------------------------------------------------------- login throttling

// loginLimiter slows down repeated failed logins.
//
// Without it, an operator password is the only thing between the internet and
// the ability to reconfigure every channel this service talks to, and a
// password is the one credential here that a human chose. The backoff is per
// source address: it does not stop a distributed attempt, and it does stop the
// one that matters, which is somebody guessing against a single exposed port.
type loginLimiter struct {
	mu      sync.Mutex
	entries map[string]*attempts
	now     func() time.Time

	// maxEntries bounds the map. An attacker rotating source addresses would
	// otherwise grow it without limit, which turns a login throttle into a
	// memory leak — the defence becoming the attack.
	maxEntries int
}

type attempts struct {
	count int
	until time.Time
}

const (
	// freeAttempts is how many failures are tolerated before the first delay.
	// Enough that a typo costs nothing, few enough that guessing is hopeless.
	freeAttempts = 5
	// maxBackoff caps the wait, so an operator who mistyped repeatedly is
	// delayed rather than locked out — there is no unlock procedure here, and
	// one that requires a restart would be a denial of service with a
	// self-inflicted trigger.
	maxBackoff = 5 * time.Minute
)

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{
		entries:    map[string]*attempts{},
		now:        func() time.Time { return time.Now().UTC() },
		maxEntries: 1024,
	}
}

// retryAfter reports how long this source must wait before trying again.
func (l *loginLimiter) retryAfter(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()

	entry, ok := l.entries[key]
	if !ok {
		return 0
	}
	if wait := entry.until.Sub(l.now()); wait > 0 {
		return wait
	}
	return 0
}

// fail records a failed attempt and returns the delay now imposed.
func (l *loginLimiter) fail(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.evictLocked()

	entry, ok := l.entries[key]
	if !ok {
		entry = &attempts{}
		l.entries[key] = entry
	}
	entry.count++

	if entry.count <= freeAttempts {
		return 0
	}

	// Doubling from one second: the sixth failure waits a second, the seventh
	// two, and so on to the cap. Fast enough that a genuine mistake is barely
	// noticeable, slow enough that guessing at any rate is pointless.
	backoff := time.Second << min(entry.count-freeAttempts-1, 20)
	if backoff > maxBackoff {
		backoff = maxBackoff
	}
	entry.until = l.now().Add(backoff)
	return backoff
}

// succeed clears a source's record after a successful login.
func (l *loginLimiter) succeed(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, key)
}

// evictLocked keeps the map bounded.
//
// Expired records go first; if that is not enough, the map is cleared
// wholesale. Dropping live records costs an attacker their accumulated delay,
// which is a smaller problem than unbounded memory — and it only happens under
// an attack that is already rotating addresses, where per-address delays are
// worth little anyway.
func (l *loginLimiter) evictLocked() {
	if len(l.entries) < l.maxEntries {
		return
	}

	now := l.now()
	for key, entry := range l.entries {
		if !now.Before(entry.until) && entry.count <= freeAttempts {
			delete(l.entries, key)
		}
	}
	if len(l.entries) >= l.maxEntries {
		l.entries = map[string]*attempts{}
	}
}
