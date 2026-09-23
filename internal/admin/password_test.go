package admin

import (
	"net/http"
	"strings"
	"testing"
)

// signedInStoredAdmin creates the administrator through the first-run page and
// returns the session cookie.
//
// This is the state `docker compose up -d` and one visit produces: the account
// lives in the database, which is the only kind whose password can be changed
// from here.
func signedInStoredAdmin(t *testing.T) (*harness, *http.Cookie, string) {
	t.Helper()

	const password = "correct-horse-battery"
	h := newUnclaimedHarness(t)

	res := h.do(t, http.MethodPost, "/admin/api/setup",
		setupRequest{Username: "ops", Password: password}, nil, false)
	if res.code != http.StatusOK {
		t.Fatalf("setup: %d %s", res.code, res.text())
	}
	if res.cookie == nil {
		t.Fatal("setup set no session cookie")
	}
	return h, res.cookie, password
}

// The password changes, and the change is the one that takes effect: the old
// one stops working and the new one starts.
func TestPassword_ChangesTheStoredAdministrator(t *testing.T) {
	h, cookie, old := signedInStoredAdmin(t)
	const next = "a-different-passphrase"

	res := h.do(t, http.MethodPost, "/admin/api/password",
		passwordRequest{Current: old, New: next}, cookie, true)
	if res.code != http.StatusOK {
		t.Fatalf("change: %d %s", res.code, res.text())
	}

	if res := h.do(t, http.MethodPost, "/admin/api/login",
		loginRequest{Username: "ops", Password: old}, nil, false); res.code != http.StatusUnauthorized {
		t.Errorf("the old password still signs in: %d", res.code)
	}
	if res := h.do(t, http.MethodPost, "/admin/api/login",
		loginRequest{Username: "ops", Password: next}, nil, false); res.code != http.StatusOK {
		t.Errorf("the new password does not sign in: %d %s", res.code, res.text())
	}
}

// Every other session ends, and this one does not.
//
// The usual reason to change a password is the belief that somebody else knows
// it. A change that leaves their session alive has not done the thing it was
// for — and throwing the operator out of the page they are looking at would
// leave them unable to tell a successful change from a failed one.
func TestPassword_EndsEveryOtherSession(t *testing.T) {
	h, cookie, old := signedInStoredAdmin(t)

	// A second sign-in, as if from another machine.
	second := h.do(t, http.MethodPost, "/admin/api/login",
		loginRequest{Username: "ops", Password: old}, nil, false).cookie
	if second == nil {
		t.Fatal("the second sign-in set no cookie")
	}

	res := h.do(t, http.MethodPost, "/admin/api/password",
		passwordRequest{Current: old, New: "a-different-passphrase"}, cookie, true)
	if res.code != http.StatusOK {
		t.Fatalf("change: %d %s", res.code, res.text())
	}

	var body struct {
		Ended   int    `json:"sessions_ended"`
		Message string `json:"message"`
	}
	res.decode(t, &body)
	if body.Ended != 1 {
		t.Errorf("ended %d sessions, want 1", body.Ended)
	}
	// The count is in the message the page shows, because "did that actually
	// lock the other person out" is the question the operator is asking.
	if !strings.Contains(body.Message, "1") {
		t.Errorf("message = %q, want the count in it", body.Message)
	}

	if res := h.do(t, http.MethodGet, "/admin/api/session", nil, second, true); res.code != http.StatusUnauthorized {
		t.Errorf("the other session survived the change: %d", res.code)
	}
	if res := h.do(t, http.MethodGet, "/admin/api/session", nil, cookie, true); res.code != http.StatusOK {
		t.Errorf("the session that made the change was ended too: %d", res.code)
	}
}

// An account named in the configuration file has no password in this database,
// so changing it here would be reporting a change that comes back on the next
// restart.
func TestPassword_RefusesAnAccountFromTheConfigurationFile(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	res := h.do(t, http.MethodPost, "/admin/api/password",
		passwordRequest{Current: testPassword, New: "a-different-passphrase"}, cookie, true)

	if res.code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", res.code, res.text())
	}
	if !strings.Contains(res.text(), "password_in_config") {
		t.Errorf("the refusal does not say where the password lives: %s", res.text())
	}

	// And the configured password still works, because nothing was written.
	if res := h.do(t, http.MethodPost, "/admin/api/login",
		loginRequest{Username: testUser, Password: testPassword}, nil, false); res.code != http.StatusOK {
		t.Errorf("the configured password stopped working: %d", res.code)
	}
}

// The current password is verified, and a wrong one is refused.
//
// This endpoint checks a password, so it is an endpoint somebody can guess
// against — it would be the one without a lock on it if it did not share the
// sign-in throttle.
func TestPassword_RefusesTheWrongCurrentPassword(t *testing.T) {
	h, cookie, _ := signedInStoredAdmin(t)

	res := h.do(t, http.MethodPost, "/admin/api/password",
		passwordRequest{Current: "not-the-password", New: "a-different-passphrase"}, cookie, true)

	if res.code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", res.code, res.text())
	}
}

// The floor is the same one the first-run page enforces. Two numbers for the
// same rule is how one of them ends up wrong.
func TestPassword_RefusesOneBelowTheFloor(t *testing.T) {
	h, cookie, old := signedInStoredAdmin(t)

	res := h.do(t, http.MethodPost, "/admin/api/password",
		passwordRequest{Current: old, New: strings.Repeat("a", minPasswordLength-1)}, cookie, true)

	if res.code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", res.code, res.text())
	}
}

// The page exists and is reachable without knowing the URL.
//
// Two halves, and they belong to different layers now. The screen itself is a
// route in the client-side router — it is reached from the account menu, and
// that is a link in web/src/components/UserMenu.tsx. What the server owes is
// that the address is a real one: a refresh on /admin/password serves the
// shell rather than a 404, and the endpoint the screen posts to is behind a
// session like everything else that changes state.
func TestPassword_IsReachable(t *testing.T) {
	h, cookie, current := signedInStoredAdmin(t)

	res := h.do(t, http.MethodGet, "/admin/password", nil, nil, false)
	if res.code != http.StatusOK {
		t.Errorf("GET /admin/password = %d, want the shell; a refresh on the page "+
			"would otherwise be a 404", res.code)
	}

	// Signed out, the change itself must refuse. A 200 here would mean the one
	// endpoint that rewrites the credential was reachable without one.
	anon := h.do(t, http.MethodPost, "/admin/api/password",
		passwordRequest{Current: "whatever", New: "a-long-enough-password"}, nil, true)
	if anon.code != http.StatusUnauthorized {
		t.Errorf("POST /admin/api/password without a session = %d, want 401", anon.code)
	}

	// And with one it answers, so the screen is not a form whose button does
	// nothing. The new password is a different one, or this would be a no-op
	// the handler might accept for the wrong reason.
	ok := h.do(t, http.MethodPost, "/admin/api/password",
		passwordRequest{Current: current, New: "a-long-enough-password"}, cookie, true)
	if ok.code != http.StatusOK {
		t.Errorf("POST /admin/api/password = %d: %s", ok.code, ok.text())
	}
}
