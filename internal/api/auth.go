package api

import (
	"net/http"
	"strings"

	"notifyrelay/internal/auth"
)

const bearerPrefix = "Bearer "

// BearerAuth returns middleware that requires a valid API key.
//
// The verification itself is constant time and lives in internal/auth so the
// SMTP inbound path can reuse it with the same guarantees.
func BearerAuth(live *Live) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r)
			// Read per request rather than captured once: a key rotated through
			// the operator surface or a SIGHUP has to take effect on the next
			// request, which is the whole point of rotating it without a
			// restart.
			if !ok || !auth.Verify(live.Keys(), token) {
				w.Header().Set("WWW-Authenticate", `Bearer realm="notifyrelay"`)
				WriteError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid API key")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
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
