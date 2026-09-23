package admin

import (
	"net/http"
	"testing"
)

// An email channel with no fixed recipient can only ever fail a test: this
// endpoint names none, and there is nowhere for the message to go.
//
// Refusing says so. The alternative is a delivery that lands in the list as
// PERMANENT, which reads like a broken channel rather than like a channel
// configured for a different job — and the operator did not cause it.
func TestSendTestNotification_RefusesAChannelWithNoRecipient(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	// No `to`: this instance exists for transactional mail only.
	cfg := map[string]any{"host": "smtp.example.com", "from": "notify@example.com"}
	if res := h.do(t, http.MethodPost, "/admin/api/channels", saveRequest{
		Name: "tx", Type: "email", Config: cfg,
	}, cookie, true); res.code != http.StatusOK {
		t.Fatalf("save channel: %d %s", res.code, res.text())
	}

	res := h.do(t, http.MethodPost, "/admin/api/channels/tx/test-notification",
		map[string]any{"title": "hi", "body": "there"}, cookie, true)

	if res.code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", res.code, res.text())
	}

	var body struct {
		Error string `json:"error"`
	}
	res.decode(t, &body)
	if body.Error != "channel_needs_recipients" {
		t.Errorf("error = %q, want channel_needs_recipients", body.Error)
	}
	if got := len(h.queue.calls()); got != 0 {
		t.Errorf("%d deliveries were queued for a refused test", got)
	}
}

// The button is off before it is clicked, not after. An operator looking at the
// channel list should not have to find out by causing a failure.
//
// The button is the client's markup now; what the server owes is the fact it is
// drawn from, per channel. `needs_recipients` is computed in the handler rather
// than in the list template, so this is the same computation — reached through
// the endpoint the list calls.
//
// Both channels are on the same list on purpose. A flag that were true for
// every channel, or false for every channel, would pass a test that only ever
// looked at one.
func TestChannels_ListReportsWhichOnesHaveNoRecipient(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	// No `to`: transactional only.
	if res := h.do(t, http.MethodPost, "/admin/api/channels", saveRequest{
		Name: "tx", Type: "email",
		Config: map[string]any{"host": "smtp.example.com", "from": "notify@example.com"},
	}, cookie, true); res.code != http.StatusOK {
		t.Fatalf("save tx: %d %s", res.code, res.text())
	}
	// With a recipient: the ordinary alert route.
	if res := h.do(t, http.MethodPost, "/admin/api/channels", saveRequest{
		Name: "oncall", Type: "email", Config: emailConfig(""),
	}, cookie, true); res.code != http.StatusOK {
		t.Fatalf("save oncall: %d %s", res.code, res.text())
	}

	var body struct {
		Channels []channelView `json:"channels"`
	}
	res := h.do(t, http.MethodGet, "/admin/api/channels", nil, cookie, false)
	if res.code != http.StatusOK {
		t.Fatalf("list channels: %d %s", res.code, res.text())
	}
	res.decode(t, &body)

	byName := map[string]channelView{}
	for _, c := range body.Channels {
		byName[c.Name] = c
	}

	if !byName["tx"].NeedsRecipients {
		t.Error("a channel with no recipient is not reported as having none; the test " +
			"button would be live and would fail when it was pressed")
	}
	if byName["oncall"].NeedsRecipients {
		t.Error("a channel with a recipient is reported as having none; its test button " +
			"would be disabled for no reason")
	}
}

// The same channel with a recipient works, and the delivery is pinned to the
// instance — the queue must not have to work that out again later.
func TestSendTestNotification_AcceptsAChannelWithARecipient(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	if res := h.do(t, http.MethodPost, "/admin/api/channels", saveRequest{
		Name: "oncall", Type: "email", Config: emailConfig(""),
	}, cookie, true); res.code != http.StatusOK {
		t.Fatalf("save channel: %d %s", res.code, res.text())
	}

	res := h.do(t, http.MethodPost, "/admin/api/channels/oncall/test-notification",
		map[string]any{"title": "hi", "body": "there"}, cookie, true)
	if res.code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", res.code, res.text())
	}

	calls := h.queue.calls()
	if len(calls) != 1 || len(calls[0].Targets) != 1 {
		t.Fatalf("queued %+v, want one target", calls)
	}
	if got := calls[0].Targets[0]; got.Channel != "oncall" || got.Target != "oncall" {
		t.Errorf("queued target %+v, want both fields naming the instance", got)
	}
}
