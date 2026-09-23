package admin

import (
	"net/http"
	"testing"
)

// checklist reads the first-run checklist's state.
//
// It is the real endpoint rather than a test hook, and the navigation reads the
// same one: the count beside "get started" is how an operator knows there is
// something left, it is on every page, and it is the one number that has to be
// right — a checklist page that disagrees with it is the bug this is looking
// for.
//
// This used to parse the number out of a rendered page. The page is the
// client-side interface now, so the number comes from the endpoint that page
// calls, which is one fewer layer between the assertion and the fact.
func checklist(t *testing.T, h *harness, cookie *http.Cookie) onboardingResponse {
	t.Helper()

	var got onboardingResponse
	res := h.do(t, http.MethodGet, "/admin/api/onboarding", nil, cookie, false)
	if res.code != http.StatusOK {
		t.Fatalf("GET /admin/api/onboarding = %d: %s", res.code, res.text())
	}
	res.decode(t, &got)
	return got
}

// remaining is the count on its own, for the tests that only care about it.
func remaining(t *testing.T, h *harness, cookie *http.Cookie) int {
	t.Helper()
	return checklist(t, h, cookie).Remaining
}

// The checklist reports what the deployment contains, not what somebody ticked.
//
// That is the whole reason it is derived. A stored checklist is right until the
// first time somebody deletes the thing that finished a step, and then it is a
// page telling an operator they are done when they are not — which is worse
// than having no page, because they will believe it.
func TestStart_StepsFollowTheDeployment(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	// A deployment with nothing in it.
	if got := remaining(t, h, cookie); got != 4 {
		t.Errorf("%d steps left on an empty deployment, want 4", got)
	}

	// A channel finishes the first.
	h.do(t, http.MethodPost, "/admin/api/channels", saveRequest{
		Name: "oncall", Type: "email", Config: emailConfig("hunter2"),
	}, cookie, true)
	if got := remaining(t, h, cookie); got != 3 {
		t.Errorf("%d steps left after a channel, want 3", got)
	}

	// A key finishes the second.
	if res := h.do(t, http.MethodPost, "/admin/api/keys",
		map[string]any{"name": "ci"}, cookie, true); res.code != http.StatusOK {
		t.Fatalf("create key: %d %s", res.code, res.text())
	}
	if got := remaining(t, h, cookie); got != 2 {
		t.Errorf("%d steps left after a key, want 2", got)
	}

	// A delivery that has not been attempted finishes the third and not the
	// fourth: the notification has been sent, and what happened to it is the
	// fourth step's business. This is the distinction the two steps exist for.
	seedDelivery(t, h, "d1", "oncall", "queued")
	if got := remaining(t, h, cookie); got != 1 {
		t.Errorf("%d steps left with a delivery still queued, want 1", got)
	}

	// A terminal one finishes the fourth — and a failure counts, because "look
	// at the result" is the step, not "it worked".
	seedDelivery(t, h, "d2", "oncall", "failed")
	if got := remaining(t, h, cookie); got != 0 {
		t.Errorf("%d steps left after a terminal delivery, want 0", got)
	}
}

// ...and it goes backwards, which is the property a stored checklist cannot
// have.
func TestStart_DeletingTheThingUnticksTheStep(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	h.do(t, http.MethodPost, "/admin/api/channels", saveRequest{
		Name: "oncall", Type: "email", Config: emailConfig("hunter2"),
	}, cookie, true)
	if got := remaining(t, h, cookie); got != 3 {
		t.Fatalf("%d steps left after a channel, want 3", got)
	}

	if res := h.do(t, http.MethodDelete, "/admin/api/channels/oncall", nil, cookie, true); res.code != http.StatusOK {
		t.Fatalf("delete: %d %s", res.code, res.text())
	}
	if got := remaining(t, h, cookie); got != 4 {
		t.Errorf("%d steps left after the only channel was deleted, want 4", got)
	}
}

// Done is the flag the navigation decides on, and it has to agree with the
// count.
//
// The sidebar offers the checklist while it is unfinished and stops once it is
// not — a "get started" link that outlives the getting started is a link that
// teaches the operator to stop reading the sidebar. That rule is applied in the
// client, from these two fields, and two fields that can disagree are two
// chances to get it wrong: an operator looking at a checklist of four ticks
// with "3 to go" beside it has been told the service is confused.
//
// The last assertion is the one worth having. "Done" is not "no steps are
// left" in the abstract — it is a conjunction over four named facts, and a
// deployment missing only the last one is the case where a bug in the flag is
// easiest to miss.
func TestStart_DoneAgreesWithTheCount(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	steps := []struct {
		name string
		do   func()
	}{
		{"a channel", func() {
			h.do(t, http.MethodPost, "/admin/api/channels", saveRequest{
				Name: "oncall", Type: "email", Config: emailConfig("hunter2"),
			}, cookie, true)
		}},
		{"a key", func() {
			h.do(t, http.MethodPost, "/admin/api/keys", map[string]any{"name": "ci"}, cookie, true)
		}},
		{"a delivery", func() { seedDelivery(t, h, "d1", "oncall", "queued") }},
		{"a result", func() { seedDelivery(t, h, "d2", "oncall", "sent") }},
	}

	for i, step := range steps {
		got := checklist(t, h, cookie)

		if got.Done != (got.Remaining == 0) {
			t.Errorf("after %d steps: done = %v and remaining = %d; the navigation and the "+
				"checklist would disagree", i, got.Done, got.Remaining)
		}
		if got.Done {
			t.Fatalf("after %d steps the checklist reported itself finished", i)
		}

		step.do()
	}

	if got := checklist(t, h, cookie); !got.Done || got.Remaining != 0 {
		t.Errorf("after every step: done = %v, remaining = %d, want true and 0",
			got.Done, got.Remaining)
	}
}

// The four facts travel individually as well as summed.
//
// The page renders a tick per step, so a client given only the count would have
// to work out which ones are done — and the day a fifth step is added, that
// arithmetic is a second implementation of a rule the server already has. The
// booleans are what makes the count derivable rather than guessed.
func TestStart_ReportsWhichStepsAreDone(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	empty := checklist(t, h, cookie)
	if empty.HasChannel || empty.HasKey || empty.HasDelivery || empty.HasResult {
		t.Errorf("an empty deployment reports steps as done: %+v", empty)
	}

	h.do(t, http.MethodPost, "/admin/api/channels", saveRequest{
		Name: "oncall", Type: "email", Config: emailConfig("hunter2"),
	}, cookie, true)

	one := checklist(t, h, cookie)
	if !one.HasChannel {
		t.Error("a deployment with a channel does not report the channel step")
	}
	if one.HasKey || one.HasDelivery || one.HasResult {
		t.Errorf("one channel finished more than one step: %+v", one)
	}
}
