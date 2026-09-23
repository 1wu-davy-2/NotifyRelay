package admin

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// setupStatus reads what the interface asks to decide which of its two
// signed-out screens to draw.
//
// The server used to answer this with a redirect: every page handler checked
// setupRequired first and sent an unclaimed deployment to /admin/setup. That
// check is in the client now, so the answer travels as data and this is where
// the tests read it. Everything they used to assert about the redirect is
// asserted about the fact behind it.
func setupStatus(t *testing.T, h *harness) setupStatusResponse {
	t.Helper()

	var got setupStatusResponse
	res := h.do(t, http.MethodGet, "/admin/api/setup", nil, nil, false)
	if res.code != http.StatusOK {
		t.Fatalf("GET /admin/api/setup = %d: %s", res.code, res.text())
	}
	res.decode(t, &got)
	return got
}

// The first-run form is what makes `docker compose up -d` a complete
// deployment. Before it, standing a deployment up meant generating a password
// hash on a workstation and copying it in — and that friction is what produces
// one secret reused across deployments.
func TestSetup_UnclaimedDeploymentOffersTheForm(t *testing.T) {
	h := newUnclaimedHarness(t)

	if got := setupStatus(t, h); !got.Required {
		t.Error("a deployment with no administrator does not offer the first-run form")
	}

	// And the screen that form lives on is reachable, at every path the
	// interface routes — a visitor who followed a link to /admin/keys should not
	// land on a sign-in form for an account that does not exist. The client does
	// the routing; what the server owes is the shell, signed out.
	for _, path := range []string{"/admin", "/admin/setup", "/admin/keys", "/admin/channels"} {
		res := h.do(t, http.MethodGet, path, nil, nil, false)
		if res.code != http.StatusOK {
			t.Errorf("GET %s: status %d, want the shell", path, res.code)
		}
	}
}

// Once an administrator exists the form is gone, not merely inert. A form that
// is still reachable after it stops working is a form somebody will try.
func TestSetup_ClosesOnceAnAdministratorExists(t *testing.T) {
	h := newUnclaimedHarness(t)

	res := h.do(t, http.MethodPost, "/admin/api/setup",
		setupRequest{Username: "ops", Password: "correct-horse-battery"}, nil, false)
	if res.code != http.StatusOK {
		t.Fatalf("creating the administrator: status %d: %s", res.code, res.text())
	}
	if res.cookie == nil {
		t.Fatal("setup did not sign the new administrator in")
	}

	if got := setupStatus(t, h); got.Required {
		t.Error("the first-run form is still offered after an administrator exists")
	}

	// And the account it created can actually sign in, which is the part that
	// would be missing if the hash were stored but never verified.
	signIn := h.do(t, http.MethodPost, "/admin/api/login",
		loginRequest{Username: "ops", Password: "correct-horse-battery"}, nil, false)
	if signIn.code != http.StatusOK {
		t.Errorf("signing in as the account setup created: status %d: %s", signIn.code, signIn.text())
	}
}

// The race the conditional write exists to lose safely: two people submitting
// the setup form at the same moment. Exactly one may win, and the loser must be
// told rather than silently replacing the winner.
func TestSetup_ASecondSubmissionDoesNotTakeTheDeployment(t *testing.T) {
	h := newUnclaimedHarness(t)

	first := h.do(t, http.MethodPost, "/admin/api/setup",
		setupRequest{Username: "ops", Password: "correct-horse-battery"}, nil, false)
	if first.code != http.StatusOK {
		t.Fatalf("the first submission failed: %d", first.code)
	}

	second := h.do(t, http.MethodPost, "/admin/api/setup",
		setupRequest{Username: "attacker", Password: "another-password-here"}, nil, false)
	if second.code != http.StatusConflict {
		t.Errorf("the second submission returned %d, want 409", second.code)
	}

	// The first administrator still works and the second one does not.
	ok := h.do(t, http.MethodPost, "/admin/api/login",
		loginRequest{Username: "ops", Password: "correct-horse-battery"}, nil, false)
	if ok.code != http.StatusOK {
		t.Errorf("the first administrator can no longer sign in: %d", ok.code)
	}
	bad := h.do(t, http.MethodPost, "/admin/api/login",
		loginRequest{Username: "attacker", Password: "another-password-here"}, nil, false)
	if bad.code != http.StatusUnauthorized {
		t.Errorf("the second submission's account exists: login returned %d, want 401", bad.code)
	}
}

func TestSetup_RejectsShortPasswordsAndEmptyUsernames(t *testing.T) {
	h := newUnclaimedHarness(t)

	for _, tc := range []struct {
		name string
		req  setupRequest
	}{
		{"short password", setupRequest{Username: "ops", Password: "short"}},
		{"empty username", setupRequest{Username: "   ", Password: "correct-horse-battery"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := h.do(t, http.MethodPost, "/admin/api/setup", tc.req, nil, false)
			if res.code != http.StatusBadRequest {
				t.Errorf("status %d, want 400", res.code)
			}
		})
	}

	// And none of those attempts left an account behind, which would lock the
	// deployment to a password nobody chose.
	if got := setupStatus(t, h); !got.Required {
		t.Error("a rejected submission closed the first-run form")
	}
}

// The floor is a boundary, so the boundary is what gets pinned.
func TestSetup_AcceptsTheFloorExactlyAndRefusesOneBelowIt(t *testing.T) {
	h := newUnclaimedHarness(t)

	short := strings.Repeat("a", minPasswordLength-1)
	if res := h.do(t, http.MethodPost, "/admin/api/setup",
		setupRequest{Username: "ops", Password: short}, nil, false); res.code != http.StatusBadRequest {
		t.Fatalf("status = %d for a password one character short, want 400: %s", res.code, res.text())
	}

	atFloor := strings.Repeat("a", minPasswordLength)
	if res := h.do(t, http.MethodPost, "/admin/api/setup",
		setupRequest{Username: "ops", Password: atFloor}, nil, false); res.code != http.StatusOK {
		t.Fatalf("status = %d for a password of exactly %d characters, want 200: %s",
			res.code, minPasswordLength, res.text())
	}
}

// The length is counted in characters, not in bytes: a passphrase in a
// non-Latin script is not a third of the password it looks like.
func TestSetup_CountsPasswordLengthInCharacters(t *testing.T) {
	h := newUnclaimedHarness(t)

	// Eight characters, twenty-four bytes.
	password := strings.Repeat("密", minPasswordLength)
	if res := h.do(t, http.MethodPost, "/admin/api/setup",
		setupRequest{Username: "ops", Password: password}, nil, false); res.code != http.StatusOK {
		t.Errorf("status = %d for %d characters in a multi-byte script, want 200: %s",
			res.code, minPasswordLength, res.text())
	}
}

// The form's own minlength has to agree with the server's floor, or the browser
// waves through a password the server then refuses — which reads as a bug in
// whichever of the two the operator happened to believe.
//
// The number is sent to the client rather than written into it, which is the
// stronger fix: there is nothing left to keep in step. What this pins is that
// the number sent is the number enforced, by submitting at it and one below —
// so a change to the constant that forgot to change the response, or the other
// way round, fails here.
func TestSetup_ReportsTheFloorItEnforces(t *testing.T) {
	h := newUnclaimedHarness(t)

	reported := setupStatus(t, h).MinPasswordLength
	if reported != minPasswordLength {
		t.Fatalf("the form is told the floor is %d and the server enforces %d",
			reported, minPasswordLength)
	}
	if reported < 1 {
		t.Fatal("the reported floor is not a usable minlength")
	}

	short := strings.Repeat("a", reported-1)
	if res := h.do(t, http.MethodPost, "/admin/api/setup",
		setupRequest{Username: "ops", Password: short}, nil, false); res.code != http.StatusBadRequest {
		t.Errorf("the form would accept %d characters and the server answered %d, want 400",
			reported-1, res.code)
	}

	atFloor := strings.Repeat("a", reported)
	if res := h.do(t, http.MethodPost, "/admin/api/setup",
		setupRequest{Username: "ops", Password: atFloor}, nil, false); res.code != http.StatusOK {
		t.Errorf("the form would refuse %d characters and the server answered %d, want 200",
			reported, res.code)
	}
}

// The message names the same number, or the operator is told the rule is one
// thing by the refusal and another by the form.
func TestSetup_RefusalNamesTheFloorItEnforces(t *testing.T) {
	h := newUnclaimedHarness(t)

	res := h.do(t, http.MethodPost, "/admin/api/setup",
		setupRequest{Username: "ops", Password: "short"}, nil, false)
	if res.code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", res.code)
	}

	want := strconv.Itoa(minPasswordLength)
	if !strings.Contains(res.text(), want) {
		t.Errorf("the refusal does not name the floor %s: %s", want, res.text())
	}
}

// A configured password hash means the operator already made the decision this
// form exists to collect, so it is not offered at all.
func TestSetup_NotOfferedWhenTheHashIsInTheConfiguration(t *testing.T) {
	h := newHarness(t, true)

	if got := setupStatus(t, h); got.Required {
		t.Error("a deployment with a configured administrator offered first-run setup")
	}
}
