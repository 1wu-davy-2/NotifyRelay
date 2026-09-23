package admin

import (
	"fmt"
	"log/slog"
	"net/http"

	"notifyrelay/internal/auth"
)

type passwordRequest struct {
	Current string `json:"current_password"`
	New     string `json:"new_password"`
}

// changePassword implements POST /admin/api/password.
//
// There was no way to change this password at all. The only routes were editing
// the database by hand or writing a hash into the configuration file and
// restarting, which is a poor answer to "I think somebody else knows it" — the
// moment this has to be easy.
//
// What it does not do is offer a way back in. Recovery needs a second
// credential or a mail path, neither of which exists here, and inventing one
// badly would be worse than the documented "you will have to edit the database".
func (h *handler) changePassword(w http.ResponseWriter, r *http.Request) {
	t := copyFor(r)
	actor := actorFrom(r.Context())

	var req passwordRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", t.ErrInvalidJSON)
		return
	}

	// The same throttle a sign-in is checked against. This endpoint verifies a
	// password, so it is an endpoint somebody can guess against — and it would
	// be the one without a lock on it.
	key := clientKey(r)
	if wait := h.limiter.retryAfter(key); wait > 0 {
		w.Header().Set("Retry-After", formatSeconds(wait))
		writeError(w, http.StatusTooManyRequests, "too_many_attempts", t.ErrTooManySignIns)
		return
	}

	// The account is the session's own actor, not a name from the body. A
	// password change that named its own target would be a way to change
	// somebody else's, and there is only one account to change.
	_, source, ok := h.authenticate(r.Context(), actor, req.Current)
	if !ok {
		h.limiter.fail(key)
		h.log.Warn("admin: a password change was refused",
			slog.String("remote", key), slog.String("actor", actor))
		writeError(w, http.StatusUnauthorized, "invalid_credentials", t.ErrBadCredentials)
		return
	}

	if len([]rune(req.New)) < minPasswordLength {
		writeError(w, http.StatusBadRequest, "invalid_request",
			fmt.Sprintf(t.ErrPasswordTooShort, minPasswordLength))
		return
	}

	// An account named in the configuration file has no password in this
	// database, and reporting success would be reporting a change that comes
	// back on the next restart.
	if source == fromConfig {
		writeError(w, http.StatusConflict, "password_in_config", t.ErrPasswordInConfig)
		return
	}
	if h.deps.Credentials == nil {
		writeError(w, http.StatusNotImplemented, "unavailable", t.ErrNoCredentialStore)
		return
	}

	hash, err := auth.HashPassword(req.New)
	if err != nil {
		h.log.Error("admin: could not hash the new password", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal", t.ErrPasswordChangeFailed)
		return
	}

	changed, err := h.deps.Credentials.SetAdminPassword(r.Context(), hash)
	if err != nil {
		h.log.Error("admin: could not store the new password", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal", t.ErrPasswordChangeFailed)
		return
	}
	if !changed {
		// The row went away between authenticating and writing. Reported rather
		// than swallowed: the operator is otherwise left believing a password
		// changed that did not.
		h.log.Error("admin: the administrator row vanished during a password change")
		writeError(w, http.StatusConflict, "no_credential", t.ErrNoCredentialStore)
		return
	}

	h.limiter.succeed(key)

	// Every other session ends. The usual reason to change a password is the
	// belief that somebody else knows it, and leaving that person's session
	// alive has not done the thing the change was for. This one is kept, so the
	// operator can see that it worked rather than being thrown at a sign-in
	// form with no explanation.
	var ended int
	if cookie, err := r.Cookie(cookieName); err == nil {
		ended = h.sessions.endOthers(cookie.Value)
	}

	h.record(r.Context(), "admin.password", actor, "changed the account password")

	writeJSON(w, http.StatusOK, map[string]any{
		"sessions_ended": ended,
		"message":        fmt.Sprintf(t.PasswordChanged, ended),
	})
}
