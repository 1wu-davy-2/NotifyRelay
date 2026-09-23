package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"notifyrelay/internal/auth"
	"notifyrelay/internal/store"
)

const bearerPrefix = "Bearer "

// keyTouchInterval is how stale a key's last_used_at has to be before a request
// bothers to update it.
//
// "When was this key last used" is the question that decides whether an old
// integration can be revoked, and it does not need to be accurate to the
// second. Writing on every request would put a database write in front of every
// notification to keep a column that precise.
const keyTouchInterval = 5 * time.Minute

// Identity is the authenticated caller.
//
// It exists because authorisation is no longer a yes/no question: a key may
// address the channels an operator configured, and may or may not be allowed to
// name recipients of its own. A boolean cannot carry that.
type Identity struct {
	// Name is the key's name. Used in messages and audit records, never for
	// authentication — the name is not a secret and not unique enough to be one.
	Name string
	// AllowedRecipients lists the address patterns this key may name. Empty
	// means none.
	AllowedRecipients []string
}

type identityKey struct{}

// withIdentity attaches the caller to the request context.
func withIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, id)
}

// identityFrom returns the authenticated caller.
//
// The second return value is false when no middleware ran, which a handler must
// treat as "no rights at all" rather than as "unrestricted": the failure mode
// of the other reading is a route that forgot the middleware and hands out
// arbitrary recipients.
func identityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(Identity)
	return id, ok
}

// BearerAuth returns middleware that requires a valid API key.
//
// Two sources, checked in that order for a reason: keys from the configuration
// file are already in memory, so the common case costs no database round trip
// and a deployment that manages its keys in the file keeps working exactly as
// it did. Keys created through the operator UI live in the database, which is
// what makes them creatable without editing a file and restarting.
//
// The constant-time comparison itself lives in internal/auth so the SMTP
// inbound path can reuse it with the same guarantees.
func BearerAuth(live *Live, keys store.APIKeys) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r)
			if !ok {
				unauthorized(w)
				return
			}

			// Read per request rather than captured once: a key rotated through
			// the operator surface or a SIGHUP has to take effect on the next
			// request, which is the whole point of rotating it without a
			// restart.
			if k, ok := auth.Identify(live.Keys(), token); ok {
				next.ServeHTTP(w, r.WithContext(withIdentity(r.Context(), Identity{
					Name:              k.Name,
					AllowedRecipients: k.AllowedRecipients,
				})))
				return
			}

			if keys != nil {
				if k, ok := lookupStoredKey(r.Context(), keys, token); ok {
					next.ServeHTTP(w, r.WithContext(withIdentity(r.Context(), Identity{
						Name:              k.Name,
						AllowedRecipients: k.AllowedRecipients,
					})))
					return
				}
			}

			unauthorized(w)
		})
	}
}

// lookupStoredKey looks the presented token up by digest.
//
// By hash rather than by comparing against every stored key: the digest is a
// preimage-resistant function of the token, so indexing on it reveals nothing
// about the tokens, and the alternative is a full scan of the key table on the
// hot path of every notification.
func lookupStoredKey(ctx context.Context, keys store.APIKeys, token string) (*store.APIKey, bool) {
	key, err := keys.FindAPIKeyByHash(ctx, auth.HashAPIKey(token))
	if err != nil || key == nil {
		// A store that cannot answer must not authenticate. Returning false
		// means a broken database takes the API down, which is the loud
		// failure; the alternative is an outage that looks like success.
		return nil, false
	}

	// Best effort. Failing to record that a key was used is not a reason to
	// reject a request that presented it correctly.
	_ = keys.TouchAPIKey(ctx, key.ID, time.Now(), keyTouchInterval)
	return key, true
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="notifyrelay"`)
	WriteError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid API key")
}

func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if len(h) <= len(bearerPrefix) || !strings.EqualFold(h[:len(bearerPrefix)], bearerPrefix) {
		return "", false
	}
	token := strings.TrimSpace(h[len(bearerPrefix):])
	if token == "" {
		return "", false
	}
	return token, true
}
