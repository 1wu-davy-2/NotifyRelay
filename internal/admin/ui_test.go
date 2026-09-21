package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"notifyrelay/internal/channel"
	"notifyrelay/internal/store"
)

// getPage fetches an HTML page with a session.
func (h *harness) getPage(t *testing.T, path string, cookie *http.Cookie) string {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, path, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: status %d: %s", path, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// The acceptance criterion for the form: what it offers is what the schema
// declares, with nothing hand-written in between.
//
// The check is on the rendered HTML rather than on an intermediate structure,
// because "the form is generated from the schema" is a claim about what the
// operator sees.
func TestChannelsPage_FormFieldsMatchTheSchemaExactly(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	page := h.getPage(t, "/admin/channels?edit=__new__", cookie)

	nameAttr := regexp.MustCompile(`name="([a-z0-9_]+)"`)

	for _, d := range channel.Descriptors() {
		group := extractTypeGroup(t, page, d.Type)
		if group == "" {
			t.Errorf("%s: no field group was rendered", d.Type)
			continue
		}

		rendered := map[string]bool{}
		for _, m := range nameAttr.FindAllStringSubmatch(group, -1) {
			rendered[m[1]] = true
		}

		for _, spec := range d.ParamSchema {
			if !rendered[spec.Name] {
				t.Errorf("%s: the schema declares %q and the form does not offer it", d.Type, spec.Name)
			}
		}

		declared := map[string]bool{}
		for _, spec := range d.ParamSchema {
			declared[spec.Name] = true
		}
		for name := range rendered {
			if !declared[name] {
				t.Errorf("%s: the form offers %q, which the schema does not declare", d.Type, name)
			}
		}
	}
}

// extractTypeGroup returns the HTML of one channel type's field group.
func extractTypeGroup(t *testing.T, page, channelType string) string {
	t.Helper()

	marker := `data-type="` + channelType + `"`
	start := strings.Index(page, marker)
	if start < 0 {
		return ""
	}
	rest := page[start:]

	// The group ends at the next type group, or at the section after the
	// configuration block. Without a bound, the last type's group would run on
	// into the allowance inputs and the test would report them as parameters
	// the schema does not declare.
	for _, boundary := range []string{`<div class="type-fields"`, `<h3>`} {
		if end := strings.Index(rest, boundary); end > 0 {
			return rest[:end]
		}
	}
	return rest
}

// Every conditional field has to carry the attributes the browser needs, or it
// is shown when it does not apply — which is how an operator fills in a box
// that has nothing to do with what they selected.
func TestChannelsPage_ConditionalFieldsCarryTheirCondition(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	page := h.getPage(t, "/admin/channels?edit=__new__", cookie)
	webhook := extractTypeGroup(t, page, "webhook")

	for _, tc := range []struct{ param, field, equals string }{
		{"token", "auth_type", "bearer"},
		{"password", "auth_type", "basic"},
		{"header_value", "auth_type", "header"},
		{"secret", "auth_type", "hmac"},
		{"signature_base64", "auth_type", "hmac"},
	} {
		frag := fieldFragment(t, webhook, tc.param)
		if !strings.Contains(frag, `data-show-if-field="`+tc.field+`"`) {
			t.Errorf("%s: no data-show-if-field", tc.param)
		}
		if !strings.Contains(frag, `data-show-if-equals="`+tc.equals+`"`) {
			t.Errorf("%s: no data-show-if-equals=%q", tc.param, tc.equals)
		}
		if !strings.Contains(frag, "hidden") {
			t.Errorf("%s: a conditional field is rendered visible", tc.param)
		}
	}

	wecom := extractTypeGroup(t, page, "wecom")
	for _, tc := range []struct{ param, equals string }{
		{"webhook_url", "webhook"},
		{"corp_secret", "app"},
		{"agent_id", "app"},
	} {
		frag := fieldFragment(t, wecom, tc.param)
		if !strings.Contains(frag, `data-show-if-field="mode"`) ||
			!strings.Contains(frag, `data-show-if-equals="`+tc.equals+`"`) {
			t.Errorf("wecom.%s: the mode condition is missing or wrong", tc.param)
		}
	}
}

// fieldFragment returns the HTML of one field's div.
func fieldFragment(t *testing.T, group, param string) string {
	t.Helper()

	start := strings.Index(group, `data-field="`+param+`"`)
	if start < 0 {
		t.Fatalf("field %q is not in the group", param)
	}
	rest := group[start:]

	if end := strings.Index(rest, `<div class="field"`); end > 0 {
		return rest[:end]
	}
	return rest
}

// The rule the schema documents, checked against the schema rather than against
// a hand-written list: a conditional parameter is required when it applies,
// unless it declares a default or is a boolean.
func TestChannelsPage_RequiredFollowsTheSchema(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	page := h.getPage(t, "/admin/channels?edit=__new__", cookie)

	for _, d := range channel.Descriptors() {
		group := extractTypeGroup(t, page, d.Type)
		for _, spec := range d.ParamSchema {
			frag := fieldFragment(t, group, spec.Name)

			want := spec.Required
			if spec.ShowIf != nil && spec.Type != channel.ParamBool && spec.Default == nil {
				want = true
			}

			got := strings.Contains(frag, `data-required="1"`)
			if got != want {
				t.Errorf("%s.%s: required marker = %v, want %v", d.Type, spec.Name, got, want)
			}
		}
	}
}

// The two cases the acceptance criterion names.
func TestChannelsPage_RequiredMarkersMatchTheAcceptanceCriterion(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	page := h.getPage(t, "/admin/channels?edit=__new__", cookie)
	webhook := extractTypeGroup(t, page, "webhook")

	requiredUnder := func(mode string) map[string]bool {
		out := map[string]bool{}
		for _, d := range channel.Descriptors() {
			if d.Type != "webhook" {
				continue
			}
			for _, spec := range d.ParamSchema {
				if spec.ShowIf == nil || spec.ShowIf.Equals != mode {
					continue
				}
				if strings.Contains(fieldFragment(t, webhook, spec.Name), `data-required="1"`) {
					out[spec.Name] = true
				}
			}
		}
		return out
	}

	underHMAC := requiredUnder("hmac")
	if !underHMAC["secret"] {
		t.Error("secret is not marked required under hmac")
	}
	for _, name := range []string{"signature_header", "signature_prefix", "signature_base64"} {
		if underHMAC[name] {
			t.Errorf("%s is marked required under hmac, but it has a usable default", name)
		}
	}

	underBasic := requiredUnder("basic")
	if !underBasic["username"] || !underBasic["password"] {
		t.Errorf("username/password are not both required under basic: %v", underBasic)
	}
}

// A credential must not reach the browser, whatever the operator opened.
func TestChannelsPage_NeverRendersACredential(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	const password = "hunter2-super-secret-value"
	if res := h.do(t, http.MethodPost, "/admin/api/channels", saveRequest{
		Name: "oncall", Type: "email", Config: emailConfig(password),
	}, cookie, true); res.code != http.StatusOK {
		t.Fatalf("save: %d %s", res.code, res.text())
	}

	for _, path := range []string{
		"/admin/channels",
		"/admin/channels?edit=oncall",
		"/admin/deliveries",
		"/admin/audit",
	} {
		if page := h.getPage(t, path, cookie); strings.Contains(page, password) {
			t.Errorf("%s contains the stored password", path)
		}
	}

	// ...but the form says a value is set, so the operator is not left
	// wondering whether saving will wipe it.
	page := h.getPage(t, "/admin/channels?edit=oncall", cookie)
	if !strings.Contains(page, "a value is stored") {
		t.Error("the form does not say that a credential is already set")
	}
	if !strings.Contains(page, `data-clear="password"`) {
		t.Error("the form offers no way to clear the stored credential")
	}
}

// The page embeds the same catalogue the API serves, so the browser can resolve
// a parameter's type without a round trip — and so a test can see that the
// schema reached the page intact.
func TestChannelsPage_EmbedsTheCatalog(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	page := h.getPage(t, "/admin/channels?edit=__new__", cookie)

	marker := `<script type="application/json" id="catalog">`
	start := strings.Index(page, marker)
	if start < 0 {
		t.Fatal("the catalogue is not embedded in the page")
	}
	rest := page[start+len(marker):]
	end := strings.Index(rest, "</script>")
	if end < 0 {
		t.Fatal("the embedded catalogue is not terminated")
	}

	var entries []struct {
		Type       string `json:"type"`
		Parameters []struct {
			Name   string `json:"name"`
			Type   string `json:"type"`
			ShowIf *struct {
				Field  string `json:"field"`
				Equals any    `json:"equals"`
			} `json:"show_if"`
			Min     *float64 `json:"min"`
			Private bool     `json:"private"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal([]byte(rest[:end]), &entries); err != nil {
		t.Fatalf("the embedded catalogue is not valid JSON: %v", err)
	}

	if len(entries) != len(channel.Descriptors()) {
		t.Errorf("embedded %d channel types, want %d", len(entries), len(channel.Descriptors()))
	}

	var sawShowIf, sawMin, sawPrivate bool
	for _, e := range entries {
		for _, p := range e.Parameters {
			sawShowIf = sawShowIf || p.ShowIf != nil
			sawMin = sawMin || p.Min != nil
			sawPrivate = sawPrivate || p.Private
		}
	}
	if !sawShowIf || !sawMin || !sawPrivate {
		t.Errorf("the embedded catalogue lost declarations: show_if=%v min=%v private=%v",
			sawShowIf, sawMin, sawPrivate)
	}
}

// Pages send the operator to the sign-in form. A browser renders a JSON 401 as
// a blank page, which reads as "the server is broken" rather than "sign in".
func TestPages_RedirectToSignInWithoutASession(t *testing.T) {
	h := newHarness(t, true)

	for _, path := range []string{"/admin/", "/admin/channels", "/admin/deliveries", "/admin/audit"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		h.handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusFound {
			t.Errorf("GET %s: status %d, want a redirect", path, rec.Code)
			continue
		}
		if loc := rec.Header().Get("Location"); loc != "/admin/login" {
			t.Errorf("GET %s: redirected to %q", path, loc)
		}
	}
}

func TestLoginPage_IsServedWithoutASession(t *testing.T) {
	h := newHarness(t, true)

	req := httptest.NewRequest(http.MethodGet, "/admin/login", nil)
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `id="login-form"`) {
		t.Error("the sign-in page has no form")
	}
}

// The stylesheet and script carry no data, and a login page that cannot load
// them looks broken in a way that suggests the server is.
func TestStatic_IsServedWithoutASession(t *testing.T) {
	h := newHarness(t, true)

	for _, path := range []string{"/admin/app.css", "/admin/app.js"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		h.handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: status %d", path, rec.Code)
		}
		if rec.Body.Len() == 0 {
			t.Errorf("GET %s: empty", path)
		}
	}
}

// ---------------------------------------------------------------- deliveries

func TestDeliveriesPage_ShowsWhatWasDelivered(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	seedDelivery(t, h, "d1", "oncall", "sent")

	page := h.getPage(t, "/admin/deliveries", cookie)
	if !strings.Contains(page, "d1") {
		t.Error("the delivery is not listed")
	}
	if !strings.Contains(page, "oncall") {
		t.Error("the channel is not shown")
	}
}

func TestDeliveryPage_ShowsTheAttemptHistory(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	seedDelivery(t, h, "d2", "oncall", "failed")

	page := h.getPage(t, "/admin/deliveries/d2", cookie)
	if !strings.Contains(page, "d2") {
		t.Error("the delivery id is not shown")
	}
	if !strings.Contains(page, "Attempts") {
		t.Error("the attempt history section is missing")
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
