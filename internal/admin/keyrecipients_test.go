package admin

import (
	"net/http"
	"strings"
	"testing"
)

// keyList reads the key list back and returns the view for one name.
func keyList(t *testing.T, h *harness, cookie *http.Cookie, name string) keyView {
	t.Helper()

	res := h.do(t, http.MethodGet, "/admin/api/keys", nil, cookie, false)
	if res.code != http.StatusOK {
		t.Fatalf("list keys: %d %s", res.code, res.text())
	}

	var body struct {
		Keys []keyView `json:"keys"`
	}
	res.decode(t, &body)

	for _, k := range body.Keys {
		if k.Name == name {
			return k
		}
	}
	t.Fatalf("key %q is not in the list: %+v", name, body.Keys)
	return keyView{}
}

// The list has to carry the allow list back, or the interface below it is
// unreachable from the screen it exists for.
//
// This used to assert on the rendered keys page: the create form's field, the
// edit dialog, the row's button, and the current value on the row. The page is
// the client-side interface now and all four of those are its own markup — what
// the server owes is the value, and a list that dropped it would leave every
// key looking unrestricted.
//
// The empty case is the half worth having. `omitempty` on a slice drops an
// empty one, so a key with no allow list and a key whose list failed to load
// are the same shape on the wire; the interface has to read "absent" as "this
// key may address nobody", and that only works if absent is what it actually
// gets.
func TestKeys_ListCarriesTheRecipientAllowList(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	if res := h.do(t, http.MethodPost, "/admin/api/keys", map[string]any{
		"name": "user-service", "allowed_recipients": []string{"@example.com", "@example.org"},
	}, cookie, true); res.code != http.StatusOK {
		t.Fatalf("create key: %d %s", res.code, res.text())
	}
	if res := h.do(t, http.MethodPost, "/admin/api/keys",
		map[string]any{"name": "unrestricted"}, cookie, true); res.code != http.StatusOK {
		t.Fatalf("create key: %d %s", res.code, res.text())
	}

	restricted := keyList(t, h, cookie, "user-service")
	if got := strings.Join(restricted.AllowedRecipients, ","); got != "@example.com,@example.org" {
		t.Errorf("allowed_recipients = %q, want both patterns in order", got)
	}

	unrestricted := keyList(t, h, cookie, "unrestricted")
	if len(unrestricted.AllowedRecipients) != 0 {
		t.Errorf("a key created without an allow list came back with %v",
			unrestricted.AllowedRecipients)
	}
}

func TestCreateKey_StoresAllowedRecipients(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	res := h.do(t, http.MethodPost, "/admin/api/keys", map[string]any{
		"name":               "user-service",
		"allowed_recipients": []string{"@example.com"},
	}, cookie, true)
	if res.code != http.StatusOK {
		t.Fatalf("create key: %d %s", res.code, res.text())
	}

	got := keyList(t, h, cookie, "user-service")
	if len(got.AllowedRecipients) != 1 || got.AllowedRecipients[0] != "@example.com" {
		t.Errorf("allowed_recipients = %v, want [@example.com]", got.AllowedRecipients)
	}
}

// The default is the safe one: a key created without a list may address nobody,
// and can only reach the destinations channels were configured with.
func TestCreateKey_WithoutAListMayAddressNobody(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	if res := h.do(t, http.MethodPost, "/admin/api/keys",
		map[string]any{"name": "ci"}, cookie, true); res.code != http.StatusOK {
		t.Fatalf("create key: %d %s", res.code, res.text())
	}

	if got := keyList(t, h, cookie, "ci"); len(got.AllowedRecipients) != 0 {
		t.Errorf("allowed_recipients = %v, want none", got.AllowedRecipients)
	}
}

// A pattern that matches nothing is the worst outcome — the key looks
// configured and quietly fails — so it is refused where it is typed.
func TestCreateKey_RefusesAMalformedPattern(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	res := h.do(t, http.MethodPost, "/admin/api/keys", map[string]any{
		"name":               "typo",
		"allowed_recipients": []string{"example.com"},
	}, cookie, true)

	if res.code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", res.code, res.text())
	}

	var body struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	res.decode(t, &body)
	if body.Error != "invalid_request" {
		t.Errorf("error = %q, want invalid_request", body.Error)
	}
	// The offending pattern is named, because "invalid pattern" alone leaves
	// the operator guessing which of several.
	if !strings.Contains(body.Message, "example.com") {
		t.Errorf("message = %q, want it to name the pattern", body.Message)
	}
}

// Toggling a key must not revoke its recipients. Absent means unchanged and
// empty means none — the distinction that keeps an enable/disable switch from
// silently breaking password resets.
func TestUpdateKey_AbsentRecipientsAreLeftAlone(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	if res := h.do(t, http.MethodPost, "/admin/api/keys", map[string]any{
		"name": "user-service", "allowed_recipients": []string{"@example.com"},
	}, cookie, true); res.code != http.StatusOK {
		t.Fatalf("create key: %d %s", res.code, res.text())
	}

	id := keyList(t, h, cookie, "user-service").ID

	// A toggle that says nothing about recipients.
	if res := h.do(t, http.MethodPost, "/admin/api/keys/"+id,
		map[string]any{"enabled": false}, cookie, true); res.code != http.StatusOK {
		t.Fatalf("disable: %d %s", res.code, res.text())
	}

	got := keyList(t, h, cookie, "user-service")
	if len(got.AllowedRecipients) != 1 {
		t.Errorf("allowed_recipients = %v after a toggle, want them kept", got.AllowedRecipients)
	}
	if got.Enabled {
		t.Error("the key should be disabled")
	}
}

// An explicit empty list is how an operator revokes the permission, and it has
// to mean that rather than "unchanged".
func TestUpdateKey_EmptyListClears(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	if res := h.do(t, http.MethodPost, "/admin/api/keys", map[string]any{
		"name": "user-service", "allowed_recipients": []string{"*"},
	}, cookie, true); res.code != http.StatusOK {
		t.Fatalf("create key: %d %s", res.code, res.text())
	}

	id := keyList(t, h, cookie, "user-service").ID

	if res := h.do(t, http.MethodPost, "/admin/api/keys/"+id,
		map[string]any{"enabled": true, "allowed_recipients": []string{}}, cookie, true); res.code != http.StatusOK {
		t.Fatalf("clear: %d %s", res.code, res.text())
	}

	if got := keyList(t, h, cookie, "user-service"); len(got.AllowedRecipients) != 0 {
		t.Errorf("allowed_recipients = %v after clearing, want none", got.AllowedRecipients)
	}
}
