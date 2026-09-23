package admin

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"notifyrelay/internal/admin/i18n"
	"notifyrelay/internal/channel"
	"notifyrelay/internal/store"
)

// The generated channel form, as the server builds it.
//
// ---------------------------------------------------------------------------
// These tests used to assert on the rendered HTML of /admin/channels. That page
// is the client-side interface now, so the form arrives as JSON and the
// assertions moved with it — the subject did not change. What is still the
// server's to get right is which fields exist, what they are called, which are
// required and when, and that no credential is among them.
//
// What left with the templates is the browser's half of each rule: whether a
// control carries a `required` attribute, whether a conditional field is
// hidden, whether a select arrives with a placeholder. Those belong to
// web/src/pages/ChannelForm.tsx and web/src/lib/schema.ts, and no Go test can
// see them. The server's contribution to each is asserted below — RequiredNow,
// ShowIfField/ShowIfEquals, EditType — which is the part a change to the
// channel schema can break.
// ---------------------------------------------------------------------------

// formFor fetches the generated form in English and indexes it by
// "<type>.<parameter>".
//
// English rather than the default, deliberately. These tests are about
// structure and content rather than about which language it is in, and asking
// for English keeps every one of them checking what it always checked — the
// labels are the schema's own, so an assertion can name them. The Chinese
// column of the parameter table has its own test, in paramcopy_test.go.
func formFor(t *testing.T, h *harness, cookie *http.Cookie, editing string) (channelFormData, map[string]fieldView) {
	t.Helper()
	return formIn(t, h, cookie, i18n.EN, editing)
}

// formIn fetches the generated form in a named language.
func formIn(t *testing.T, h *harness, cookie *http.Cookie, lang i18n.Lang, editing string) (channelFormData, map[string]fieldView) {
	t.Helper()

	path := "/admin/api/channel-form?" + i18n.Param + "=" + string(lang)
	if editing != "" {
		path += "&name=" + editing
	}

	var got channelFormData
	res := h.do(t, http.MethodGet, path, nil, cookie, false)
	if res.code != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", path, res.code, res.text())
	}
	res.decode(t, &got)

	byName := map[string]fieldView{}
	for _, f := range got.Forms {
		for _, field := range f.Fields {
			byName[f.Type+"."+field.Name] = field
		}
	}
	return got, byName
}

// The acceptance criterion for the form: what it offers is what the schema
// declares, with nothing hand-written in between.
//
// Set equality in both directions. A missing field is a parameter an operator
// cannot set; an extra one is a box that posts a name the server does not know,
// which the channel's own validation refuses at save time with a message about
// a parameter nobody typed.
func TestChannelForm_OffersEveryParameterAndNothingElse(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	got, _ := formFor(t, h, cookie, "")

	byType := map[string][]string{}
	for _, f := range got.Forms {
		names := make([]string, 0, len(f.Fields))
		for _, field := range f.Fields {
			names = append(names, field.Name)
		}
		byType[f.Type] = names
	}

	if len(byType) != len(channel.Descriptors()) {
		t.Errorf("the form carries %d channel types, the schema declares %d",
			len(byType), len(channel.Descriptors()))
	}

	for _, d := range channel.Descriptors() {
		names, ok := byType[d.Type]
		if !ok {
			t.Errorf("%s: no field set was generated", d.Type)
			continue
		}

		offered := map[string]bool{}
		for _, name := range names {
			offered[name] = true
		}
		declared := map[string]bool{}
		for _, spec := range d.ParamSchema {
			declared[spec.Name] = true
			if !offered[spec.Name] {
				t.Errorf("%s: the schema declares %q and the form does not offer it", d.Type, spec.Name)
			}
		}
		for _, name := range names {
			if !declared[name] {
				t.Errorf("%s: the form offers %q, which the schema does not declare", d.Type, name)
			}
		}
	}
}

// Every conditional field has to carry the condition that governs it, or the
// client cannot tell when it applies — and an operator fills in a box that has
// nothing to do with what they selected.
//
// This is the whole of what the server can say about it. Whether the field is
// hidden while the condition is false is decided in the browser, from these two
// strings.
func TestChannelForm_CarriesEveryCondition(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	_, byName := formFor(t, h, cookie, "")

	cases := []struct{ key, field, equals string }{
		{"webhook.token", "auth_type", "bearer"},
		{"webhook.password", "auth_type", "basic"},
		{"webhook.header_value", "auth_type", "header"},
		{"webhook.secret", "auth_type", "hmac"},
		{"webhook.signature_base64", "auth_type", "hmac"},
		{"wecom.webhook_url", "mode", "webhook"},
		{"wecom.corp_secret", "mode", "app"},
		{"wecom.agent_id", "mode", "app"},
	}

	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			field, ok := byName[tc.key]
			if !ok {
				t.Fatalf("%s is not in the generated form", tc.key)
			}
			if field.ShowIfField != tc.field || field.ShowIfEquals != tc.equals {
				t.Errorf("show_if = %q/%q, want %q/%q",
					field.ShowIfField, field.ShowIfEquals, tc.field, tc.equals)
			}
		})
	}
}

// The rule the schema documents, checked against the schema rather than against
// a hand-written list: a conditional parameter is required when it applies,
// unless it declares a default or is a boolean.
//
// This is the rule the client cannot re-derive — see the note above
// channelFormData — so it is asserted here, on the wire, rather than left to
// the client to get right from a schema it cannot see all of.
func TestChannelForm_RequiredFollowsTheSchema(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	_, byName := formFor(t, h, cookie, "")

	var checked int
	for _, d := range channel.Descriptors() {
		for _, spec := range d.ParamSchema {
			want := spec.Required
			if spec.ShowIf != nil && spec.Type != channel.ParamBool && spec.Default == nil {
				want = true
			}

			field, ok := byName[d.Type+"."+spec.Name]
			if !ok {
				t.Errorf("%s.%s is not in the generated form", d.Type, spec.Name)
				continue
			}
			checked++

			if field.Required != want {
				t.Errorf("%s.%s: required = %v, want %v", d.Type, spec.Name, field.Required, want)
			}
		}
	}

	if checked == 0 {
		t.Fatal("no parameter was checked; this test asserts nothing")
	}
}

// RequiredNow is the server's half of a rule about the browser.
//
// A control that is always on screen can carry a `required` attribute in the
// markup. A conditional one cannot: its attribute has to be set as it appears
// and cleared as it goes, because a hidden input that is still required is a
// form that cannot be submitted and that blames a box nobody can see. So the
// server marks the first kind and only the first kind, and the client is left
// with the case that genuinely needs a running decision.
//
// Both halves matter and they fail in opposite directions, which is why this
// asserts an equivalence rather than a list.
func TestChannelForm_RequiredNowIsTheAlwaysShownHalf(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	_, byName := formFor(t, h, cookie, "")

	var alwaysShown, conditional int
	for key, field := range byName {
		if field.ShowIfField == "" {
			alwaysShown++
			if field.RequiredNow != field.Required {
				t.Errorf("%s: always shown, required = %v but required_now = %v",
					key, field.Required, field.RequiredNow)
			}
			continue
		}

		conditional++
		if field.RequiredNow {
			t.Errorf("%s: conditional and marked required_now; the client would render a "+
				"required control for a field that is not on screen", key)
		}
	}

	if alwaysShown == 0 || conditional == 0 {
		t.Fatalf("the form has %d always-shown and %d conditional fields; "+
			"this test needs both to assert anything", alwaysShown, conditional)
	}
}

// The two cases the acceptance criterion names, spelled out.
//
// The rule above is checked against the schema, which means it would keep
// passing if the schema itself changed to something nobody wanted. These are
// the outcomes an operator would notice.
func TestChannelForm_RequiredMatchesTheAcceptanceCriterion(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	_, byName := formFor(t, h, cookie, "")

	// Under hmac the signature secret is required and its three companions are
	// not: each of them has a usable default, and the schema says so with a
	// value that JSON cannot carry. This is the case that made the endpoint
	// necessary in the first place.
	if !byName["webhook.secret"].Required {
		t.Error("webhook.secret is not required under hmac")
	}
	for _, name := range []string{
		"webhook.signature_header",
		"webhook.signature_prefix",
		"webhook.signature_base64",
	} {
		if byName["webhook."+name].Required {
			t.Errorf("webhook.%s is required, but it has a usable default", name)
		}
	}

	// Under basic both halves of the credential are required: there is no
	// default username and no default password.
	if !byName["webhook.username"].Required || !byName["webhook.password"].Required {
		t.Error("webhook.username/password are not both required under basic")
	}
}

// A create is not an edit, and the form has to say which it is.
//
// EditType is the half that decides what the operator sees: the client picks
// the field set from it, so a create that arrived carrying a type would open on
// somebody else's boxes. Empty is the answer for a create, and it is what the
// type picker keys off to offer a placeholder instead of preselecting whichever
// type sorts first — the fastest way through the form being to accept a choice
// nobody made.
//
// Editing is the name that was asked for, empty when none was. It is not the
// create/edit flag: the client's own sentinel for that travels the other way,
// in the save body, where the server compares it against the name to tell a
// rename from a collision.
func TestChannelForm_ACreateIsNotAnEdit(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	created, _ := formFor(t, h, cookie, "")
	if created.Editing != "" {
		t.Errorf("editing = %q, want empty for a create", created.Editing)
	}
	if created.EditType != "" {
		t.Errorf("a create arrived with edit_type %q; the client would open on a type "+
			"nobody chose", created.EditType)
	}
	if created.Missing {
		t.Error("a create was reported as a link to a channel that is not there")
	}

	if res := h.do(t, http.MethodPost, "/admin/api/channels", saveRequest{
		Name: "oncall", Type: "email", Config: emailConfig("hunter2"),
	}, cookie, true); res.code != http.StatusOK {
		t.Fatalf("save: %d %s", res.code, res.text())
	}

	edited, _ := formFor(t, h, cookie, "oncall")
	if edited.Editing != "oncall" {
		t.Errorf("editing = %q, want oncall", edited.Editing)
	}
	if edited.EditType != "email" {
		t.Errorf("edit_type = %q, want email; editing a channel would open on the wrong "+
			"field set and saving would change its type", edited.EditType)
	}
	if edited.Missing {
		t.Error("an existing channel was reported as missing")
	}
}

// A link to a channel that is not there is reported, not rendered blank.
//
// The form used to be rendered anyway: the lookup returned nil, the per-type
// block kept its blank values, and the page showed an empty form headed "Edit X"
// whose save would create X. An operator following a stale link got a form that
// looked like an edit and was a create, which is the kind of thing you find out
// about afterwards.
func TestChannelForm_AMissingChannelIsReported(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	got, _ := formFor(t, h, cookie, "never-existed")
	if !got.Missing {
		t.Error("a link to a channel that does not exist was not reported as missing")
	}
}

// A credential must not reach the browser, whatever the operator opened.
//
// The channel's own configuration is what the form is built from, and it holds
// the credential in clear text — buildField is the only thing standing between
// that map and this response. TestChannelFormNeverSendsAPrivateValue covers the
// endpoint; this covers the other half, which is that the operator is told a
// value exists so that saving does not silently wipe it.
func TestChannelForm_ReportsAStoredCredentialWithoutSendingIt(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	const password = "hunter2-super-secret-value"
	if res := h.do(t, http.MethodPost, "/admin/api/channels", saveRequest{
		Name: "oncall", Type: "email", Config: emailConfig(password),
	}, cookie, true); res.code != http.StatusOK {
		t.Fatalf("save: %d %s", res.code, res.text())
	}

	got, byName := formFor(t, h, cookie, "oncall")

	if !got.EditSecrets["password"] {
		t.Error("edit_secrets does not report the stored credential; the form would show " +
			"an empty box and saving would look like it was clearing it")
	}
	if field := byName["email.password"]; !field.Private || !field.IsSet {
		t.Errorf("email.password: private = %v, is_set = %v, want both true",
			field.Private, field.IsSet)
	}
	if field := byName["email.password"]; field.Value != "" {
		t.Errorf("email.password carries the stored value: %q", field.Value)
	}
}

// The shell carries no channel data, which is not obvious from where it is
// stored.
//
// The shell is a file read at startup and written back byte for byte, so it
// cannot contain an operator's configuration today. This asserts it anyway,
// because the tempting change is to fill something in server-side — the theme
// and the language are already substituted into it, and the next field is one
// edit away — and the failure would be a credential in a document served to
// anybody who asks, signed in or not.
func TestTheShellCarriesNoChannelData(t *testing.T) {
	requireShell(t)

	h := newHarness(t, true)
	cookie := h.signIn(t)

	const password = "hunter2-super-secret-value"
	if res := h.do(t, http.MethodPost, "/admin/api/channels", saveRequest{
		Name: "oncall", Type: "email", Config: emailConfig(password),
	}, cookie, true); res.code != http.StatusOK {
		t.Fatalf("save: %d %s", res.code, res.text())
	}

	// Signed out, because that is the request that matters: the shell answers
	// anybody, and an operator's configuration must not be in the answer.
	for _, path := range []string{"/admin/", "/admin/channels"} {
		res := h.do(t, http.MethodGet, path, nil, nil, false)
		for _, secret := range []string{password, "oncall"} {
			if strings.Contains(res.text(), secret) {
				t.Errorf("GET %s: the shell carries %q", path, secret)
			}
		}
	}
}

// ---------------------------------------------------------------- deliveries

// The delivery read path, which the two page tests used to be the only coverage
// of.
//
// They asserted that a seeded delivery appears on /admin/deliveries and that
// its attempt history appears on /admin/deliveries/{id}. Both pages are the
// client-side interface now, and there was no API-level test behind them — so
// deleting them would have left the whole read path uncovered rather than
// leaving it covered elsewhere. Rewritten rather than removed.
func TestDeliveries_ListShowsWhatWasDelivered(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	seedDelivery(t, h, "d1", "oncall", "sent")

	var got struct {
		Deliveries []deliveryView `json:"deliveries"`
	}
	res := h.do(t, http.MethodGet, "/admin/api/deliveries", nil, cookie, false)
	if res.code != http.StatusOK {
		t.Fatalf("GET /admin/api/deliveries = %d: %s", res.code, res.text())
	}
	res.decode(t, &got)

	if len(got.Deliveries) != 1 {
		t.Fatalf("got %d deliveries, want 1", len(got.Deliveries))
	}
	if got.Deliveries[0].ID != "d1" {
		t.Errorf("id = %q, want d1", got.Deliveries[0].ID)
	}
	if got.Deliveries[0].Target != "oncall" {
		t.Errorf("target = %q, want oncall", got.Deliveries[0].Target)
	}
}

// The detail carries the attempt history, and the two are different lifetimes
// that have to arrive together — the delivery row is one record, the attempts
// are a log that grows. A client that had to fetch them separately would show a
// page that is wrong between the two requests.
func TestDeliveries_DetailCarriesTheAttemptHistory(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	seedDelivery(t, h, "d2", "oncall", "failed")

	var got struct {
		Delivery deliveryView  `json:"delivery"`
		Attempts []attemptView `json:"attempts"`
	}
	res := h.do(t, http.MethodGet, "/admin/api/deliveries/d2", nil, cookie, false)
	if res.code != http.StatusOK {
		t.Fatalf("GET /admin/api/deliveries/d2 = %d: %s", res.code, res.text())
	}
	res.decode(t, &got)

	if got.Delivery.ID != "d2" {
		t.Errorf("delivery.id = %q, want d2", got.Delivery.ID)
	}
	if got.Delivery.Target != "oncall" {
		t.Errorf("delivery.target = %q, want oncall", got.Delivery.Target)
	}
	// Empty rather than absent: a delivery with no attempts yet is a normal
	// state, and a null here makes every client write the same nil check.
	if got.Attempts == nil {
		t.Error("attempts is null; a delivery with none yet should answer with an empty list")
	}
}

// seedDelivery writes a delivery straight to the store, which is what the
// queue would have done.
func seedDelivery(t *testing.T, h *harness, id, target, status string) {
	t.Helper()

	now := time.Now().UTC()
	if err := h.store.Enqueue(context.Background(), []*store.Delivery{{
		ID: id, RequestID: "req-" + id, Target: target, ChannelType: "email",
		Status:        store.Status(status),
		NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
	}}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
}
