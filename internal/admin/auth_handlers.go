package admin

import (
	"crypto/subtle"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"
)

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type sessionResponse struct {
	Actor     string `json:"actor"`
	ExpiresIn int    `json:"expires_in_seconds"`
}

// login starts a session.
func (h *handler) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "the request body is not valid JSON")
		return
	}

	key := clientKey(r)

	if wait := h.limiter.retryAfter(key); wait > 0 {
		w.Header().Set("Retry-After", formatSeconds(wait))
		writeError(w, http.StatusTooManyRequests, "too_many_attempts",
			"too many failed sign-in attempts; try again later")
		return
	}

	if !h.authenticate(req.Username, req.Password) {
		h.limiter.fail(key)
		h.log.Warn("admin: failed sign-in",
			slog.String("remote", key),
			slog.String("username", req.Username),
		)
		// One message for both a wrong username and a wrong password: telling
		// them apart tells an attacker which half they already have.
		writeError(w, http.StatusUnauthorized, "invalid_credentials",
			"the username or password is not correct")
		return
	}

	id, err := h.sessions.create(h.deps.Config.Username)
	if err != nil {
		h.log.Error("admin: could not create a session", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal", "the session could not be created")
		return
	}
	h.limiter.succeed(key)

	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    id,
		Path:     "/admin",
		HttpOnly: true,
		Secure:   cookieSecure(r),
		// Lax rather than Strict: following a link to the UI from a chat
		// message should land on the dashboard, not on a login page that works
		// after a refresh. Cross-site POSTs still do not carry the cookie, and
		// the CSRF header is required on top of that.
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(h.deps.Config.SessionTTL.Std().Seconds()),
	})

	h.log.Info("admin: signed in", slog.String("actor", h.deps.Config.Username))
	writeJSON(w, http.StatusOK, sessionResponse{
		Actor:     h.deps.Config.Username,
		ExpiresIn: int(h.deps.Config.SessionTTL.Std().Seconds()),
	})
}

// authenticate checks a username and password in constant time.
//
// Both halves are compared with subtle.ConstantTimeCompare even though the
// username is not a secret: a comparison that returns early on the first
// differing byte turns "guess the password" into "guess the password one byte
// at a time", and the username is the part an attacker usually already knows.
func (h *handler) authenticate(username, password string) bool {
	wantUser := []byte(h.deps.Config.Username)
	gotUser := []byte(username)

	userOK := subtle.ConstantTimeCompare(gotUser, wantUser) == 1

	// The password is always verified, even when the username is wrong, so the
	// response time does not reveal whether the username was right. An unset
	// hash verifies nothing, which is the correct behaviour for a deployment
	// whose configuration failed to parse.
	passwordOK := h.password.Verify(password)

	return userOK && passwordOK
}

// logout ends the session.
func (h *handler) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(cookieName); err == nil {
		h.sessions.end(cookie.Value)
	}

	// The cookie is cleared with the same attributes it was set with, or the
	// browser keeps the original.
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/admin",
		HttpOnly: true,
		Secure:   cookieSecure(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"signed_out": true})
}

// whoami reports the current session.
func (h *handler) whoami(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, sessionResponse{
		Actor:     actorFrom(r.Context()),
		ExpiresIn: int(h.deps.Config.SessionTTL.Std().Seconds()),
	})
}

// clientKey identifies a source for the login throttle.
//
// The remote address, not the username: throttling by username would let an
// attacker lock a real operator out by failing logins against their name.
func clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// formatSeconds renders a Retry-After value. The header is defined as a number
// of seconds, and "5s" is not one — a client that parses it strictly will read
// zero and retry immediately.
func formatSeconds(d time.Duration) string {
	secs := int(d.Round(time.Second).Seconds())
	if secs < 1 {
		secs = 1
	}
	return strconv.Itoa(secs)
}
