package admin

import (
	"context"
	"crypto/subtle"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"notifyrelay/internal/auth"
	"notifyrelay/internal/store"
)

// First-run setup.
//
// The service ships able to start with no administrator configured, because
// requiring a password hash in the file meant generating one on a workstation
// and copying it in — and that friction is what produces one secret reused
// across deployments, or a hash pasted into a ticket.
//
// The cost of that convenience is a window: between the service starting and
// somebody claiming it, whoever reaches the port first becomes the
// administrator. Three things keep that window as small as it can be without
// giving the convenience back:
//
//   - the page exists only while no administrator does, and closes permanently
//     the moment one is created;
//   - it is rate-limited on the same per-source budget as sign-in, so it cannot
//     be used to grind out accounts;
//   - the service logs a warning at startup naming the page, so an operator who
//     did not expect it finds out from the log rather than from the audit trail.
//
// What it does not do is bind to localhost or require a token from the logs.
// Either would close the window completely, and either would also mean the
// container cannot be set up from another machine — which is the deployment
// this exists for.

// minPasswordLength is the only rule enforced on the operator's password.
//
// A length floor and nothing else: composition rules (a digit, a symbol, mixed
// case) reliably produce Password1! and are worse than a longer passphrase.
//
// Eight is short for an account that can reconfigure where every notification in
// the estate goes, and it is worth saying so rather than pretending otherwise.
// It is the floor because the alternative behaved worse in practice: a higher
// minimum is what puts a password on a sticky note beside the machine, and this
// account is reached over an internal address by the person who runs the
// service. The control doing the real work against guessing is the per-source
// backoff on sign-in, not this number.
const minPasswordLength = 8

type setupRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// setupRequired reports whether this deployment still has no administrator.
//
// A configured password hash counts as an administrator even before anyone has
// signed in with it: an operator who wrote a hash into the file has already
// made the decision this page exists to collect.
func (h *handler) setupRequired(ctx context.Context) (bool, error) {
	if strings.TrimSpace(h.deps.Config.PasswordHash) != "" {
		return false, nil
	}
	if h.deps.Credentials == nil {
		// Nowhere to store one, so offering the page would be offering a form
		// that cannot succeed.
		return false, nil
	}

	credential, err := h.deps.Credentials.GetAdminCredential(ctx)
	if err != nil {
		return false, err
	}
	return credential == nil, nil
}

// setupStatusResponse is what GET /admin/api/setup answers.
type setupStatusResponse struct {
	Required bool `json:"required"`

	// MinPasswordLength is the floor the server enforces, sent rather than
	// written down again in the client.
	//
	// It is here because the alternative was three copies of the number in
	// three places — this constant, the message that names it, and the form's
	// own minlength — and a browser that waves through a password the server
	// then refuses reads as a bug in whichever of the two the operator happened
	// to believe. Sending it makes the form's copy derived rather than
	// remembered.
	//
	// The password-change form is not covered by this and cannot be: it is
	// behind a session, and a client that had to ask before it could draw a
	// field would be a form that appears a round trip late. Its floor is the
	// same constant, and the test that pins it lives in password_test.go.
	MinPasswordLength int `json:"min_password_length"`
}

// setupStatus implements GET /admin/api/setup.
//
// ---------------------------------------------------------------------------
// It exists because the sign-in screen is client-side and has to know which of
// the two forms to draw before it has a session to ask with. The server used to
// answer that question with a redirect: every page handler checked setupRequired
// first and sent an unclaimed deployment to /admin/setup. That check has moved
// into the client, so the client needs the fact rather than the redirect.
//
// Public, and it has to be — it is the question an anonymous visitor asks. It
// discloses one bit: whether this deployment has an administrator yet. That bit
// is already public in the strongest sense, because the first-run form it
// controls is reachable by whoever asks first; the whole design of first-run
// setup accepts that window and bounds it by closing the form the moment an
// account exists. See the note at the top of this file.
//
// It reports the answer and nothing about how it was reached, so a store that
// cannot be read is a 500 with no body — see below. Answering false there would
// be the dangerous direction: false means "sign in", and there is nothing to
// sign in to.
// ---------------------------------------------------------------------------
func (h *handler) setupStatus(w http.ResponseWriter, r *http.Request) {
	required, err := h.setupRequired(r.Context())
	if err != nil {
		h.log.Error("admin: could not tell whether setup is needed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal", copyFor(r).ErrAdminUnreadable)
		return
	}

	writeJSON(w, http.StatusOK, setupStatusResponse{
		Required:          required,
		MinPasswordLength: minPasswordLength,
	})
}

// createAdministrator implements POST /admin/api/setup.
func (h *handler) createAdministrator(w http.ResponseWriter, r *http.Request) {
	t := copyFor(r)

	var req setupRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", t.ErrInvalidJSON)
		return
	}

	key := clientKey(r)
	if wait := h.limiter.retryAfter(key); wait > 0 {
		w.Header().Set("Retry-After", formatSeconds(wait))
		writeError(w, http.StatusTooManyRequests, "too_many_attempts", t.ErrTooManyAttempts)
		return
	}

	username := strings.TrimSpace(req.Username)
	if username == "" {
		h.limiter.fail(key)
		writeError(w, http.StatusBadRequest, "invalid_request", t.ErrUsernameRequired)
		return
	}
	if len([]rune(req.Password)) < minPasswordLength {
		h.limiter.fail(key)
		writeError(w, http.StatusBadRequest, "invalid_request",
			fmt.Sprintf(t.ErrPasswordTooShort, minPasswordLength))
		return
	}

	if h.deps.Credentials == nil {
		writeError(w, http.StatusNotImplemented, "unavailable", t.ErrNoCredentialStore)
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		h.log.Error("admin: could not hash the new password", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal", t.ErrAccountCreateFailed)
		return
	}

	created, err := h.deps.Credentials.CreateAdminCredential(r.Context(), &store.AdminCredential{
		Username:     username,
		PasswordHash: hash,
		CreatedAt:    time.Now().UTC(),
	})
	if err != nil {
		h.log.Error("admin: could not create the administrator", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal", t.ErrAccountCreateFailed)
		return
	}
	if !created {
		// Somebody else got there between the page loading and this submission.
		// Reported as a conflict rather than as success: the caller is not the
		// administrator, and letting them believe otherwise would have them
		// sign in with a password that was never stored.
		h.log.Warn("admin: a setup submission lost the race", slog.String("remote", key))
		writeError(w, http.StatusConflict, "already_configured", t.ErrAlreadyConfigured)
		return
	}

	h.limiter.succeed(key)
	h.log.Info("admin: administrator created through first-run setup",
		slog.String("remote", key),
		slog.String("username", username),
	)

	// Signed in immediately: the operator has just proved they can reach the
	// page and chosen a password, and bouncing them to a sign-in form to retype
	// it adds a step without adding a check.
	id, err := h.sessions.create(username)
	if err != nil {
		h.log.Error("admin: could not create a session", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal", t.ErrSessionCreateFailed)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    id,
		Path:     "/admin",
		HttpOnly: true,
		Secure:   cookieSecure(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(h.deps.Config.SessionTTL.Std().Seconds()),
	})

	writeJSON(w, http.StatusOK, sessionResponse{
		Actor:     username,
		ExpiresIn: int(h.deps.Config.SessionTTL.Std().Seconds()),
	})
}

// verifyConfigured checks the credential named in the configuration file.
func (h *handler) verifyConfigured(username, password string) bool {
	if strings.TrimSpace(h.deps.Config.PasswordHash) == "" {
		return false
	}
	userOK := subtle.ConstantTimeCompare([]byte(username), []byte(h.deps.Config.Username)) == 1
	// Verified even when the username is wrong, so the response time does not
	// reveal whether the username was right.
	passwordOK := h.password.Verify(password)
	return userOK && passwordOK
}

// verifyStored checks the administrator created through the setup page,
// returning the stored username as the actor when it matches.
//
// The actor comes from the stored row rather than from the configuration: an
// account created through the setup page has a username of its own, and a
// session labelled with the configured one would put the wrong name against
// every audited action.
func (h *handler) verifyStored(ctx context.Context, username, password string) (string, bool) {
	if h.deps.Credentials == nil {
		return "", false
	}

	credential, err := h.deps.Credentials.GetAdminCredential(ctx)
	if err != nil || credential == nil {
		// A store that cannot answer authenticates nobody. Reporting the error
		// as "no such user" would be the same answer, so it is logged and the
		// caller is told no.
		if err != nil {
			h.log.Error("admin: could not read the administrator account", slog.String("error", err.Error()))
		}
		return "", false
	}

	userOK := subtle.ConstantTimeCompare([]byte(username), []byte(credential.Username)) == 1

	// A hash that will not parse verifies nothing, which is the right answer
	// for a row that has been edited by hand.
	hash, err := auth.ParsePasswordHash(credential.PasswordHash)
	passwordOK := err == nil && hash.Verify(password)

	if !userOK || !passwordOK {
		return "", false
	}
	return credential.Username, true
}
