package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"notifyrelay/internal/admin/i18n"
)

// These tests are about the contract between the built frontend and the server
// that serves it. Both halves are written by hand in different languages, and
// the failure mode when they drift is silent: the page loads, and the theme is
// wrong, or a screen reader reads English in a Chinese voice, and nothing
// anywhere reports a problem.
//
// The assertions are on the opening <html> tag rather than on the document as a
// whole, and that is not fussiness. The first version of applyPreferences
// replaced the attribute strings, the shell's own comment quoted those strings,
// and the substitution rewrote the prose while leaving the tag alone. A test
// that asked "does the document contain lang=en" would have passed.

// shellFor builds a request carrying a theme cookie and a language, the way
// withLang and the browser would.
func shellFor(theme string, lang i18n.Lang) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	if theme != "" {
		r.AddCookie(&http.Cookie{Name: themeCookie, Value: theme})
	}
	return r.WithContext(context.WithValue(r.Context(), langKey{}, lang))
}

// requireShell skips rather than fails when the frontend has not been built.
//
// dist/ is a build artefact and is not committed, so a checkout that has only
// ever run `go test` has no shell to inspect. Skipping is the honest answer:
// the test cannot make a claim about a file that does not exist, and failing
// would mean the backend could not be tested without a Node toolchain.
func requireShell(t *testing.T) {
	t.Helper()
	if distMissing {
		t.Skip("frontend not built; run `npm run build` in web/ to run this")
	}
}

// headOf returns the document's opening <html> tag.
func headOf(t *testing.T, shell []byte) string {
	t.Helper()
	m := shellHead.Find(shell)
	if m == nil {
		t.Fatal("the shell has no opening <html> tag for the server to replace")
	}
	return string(m)
}

// The shell has to have something to replace, in a shape the pattern matches.
//
// A rewrite of index.html that put the tag on a shared line with something
// else, or dropped it, would leave applyPreferences matching nothing — and the
// page would still load, in the default theme, in the default language.
func TestShellHasAnOpeningTagTheServerCanReplace(t *testing.T) {
	requireShell(t)

	tag := headOf(t, distShell)
	if !strings.Contains(tag, "lang=") || !strings.Contains(tag, "data-theme=") {
		t.Errorf("the opening tag carries no lang/data-theme to replace: %s", tag)
	}
}

// The tag the server writes is the tag the interface reads, and nothing else in
// the document is touched.
//
// The comment case is the one that regressed: web/index.html explains itself
// above the tag and quotes both attributes while doing so. If the substitution
// ever goes back to replacing attribute strings, this fails on the comment
// rather than on the tag.
func TestApplyPreferencesRewritesTheTagAndNotTheProse(t *testing.T) {
	requireShell(t)

	got := string(applyPreferences(distShell, shellFor("light", i18n.EN)))
	tag := headOf(t, []byte(got))

	if !strings.Contains(tag, `lang="en"`) {
		t.Errorf("an English request did not reach the lang attribute: %s", tag)
	}
	if !strings.Contains(tag, `data-theme="light"`) {
		t.Errorf("a light theme cookie did not reach the theme attribute: %s", tag)
	}

	// The body still explains itself in the original language. If the
	// substitution rewrote the comment instead of the tag, these would be the
	// strings that changed and the assertions above would be the ones failing —
	// which is the ordering this test exists to pin.
	if n := strings.Count(got, `lang="en"`); n != 1 {
		t.Errorf("lang=en appears %d times; the substitution rewrote more than the tag", n)
	}
}

// The default is left alone, and the shared slice is not mutated.
//
// The second half is the one worth pinning: distShell is a package-level slice
// shared by every request, so an in-place edit would leak one operator's theme
// into everybody else's page — and would do it intermittently, which is the
// worst kind of bug to be handed.
func TestApplyPreferencesLeavesTheDefaultAndTheOriginalAlone(t *testing.T) {
	requireShell(t)

	before := string(distShell)

	got := string(applyPreferences(distShell, shellFor("light", i18n.ZH)))
	if got != before {
		t.Error("a light, Chinese request changed the shell")
	}
	if string(distShell) != before {
		t.Fatal("applyPreferences mutated the shared shell")
	}
}

// Anything unrecognised is the default rather than an error.
func TestThemeOfFallsBackToLight(t *testing.T) {
	cases := []struct {
		name  string
		theme string
		want  string
	}{
		{"no cookie", "", "light"},
		{"light", "light", "light"},
		{"dark", "dark", "dark"},
		{"something else entirely", "solarized", "light"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := themeOf(shellFor(tc.theme, i18n.ZH)); got != tc.want {
				t.Errorf("themeOf = %q, want %q", got, tc.want)
			}
		})
	}
}

// The bundle and the stylesheet the shell asks for have to actually be served.
//
// This is the test that would have caught the first version of registerSPA,
// which used http.StripPrefix over a route inside a mounted router. Every part
// of that looked right in isolation: the route matched, the file server was
// pointed at the right directory, and the page was served. The bundle 404'd,
// and the interface rendered as an empty div — which is a blank white page and
// no explanation.
//
// It goes through the harness rather than calling the handler directly, because
// the mount is the thing that was wrong.
func TestAssetsAreServedThroughTheMount(t *testing.T) {
	requireShell(t)

	h := newHarness(t, true)

	cases := []struct {
		name    string
		pattern *regexp.Regexp
		wantCT  string
	}{
		{"bundle", regexp.MustCompile(`src="(/admin/assets/[^"]+\.js)"`), "javascript"},
		{"stylesheet", regexp.MustCompile(`href="(/admin/assets/[^"]+\.css)"`), "text/css"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.pattern.FindSubmatch(distShell)
			if m == nil {
				t.Fatalf("the shell references no %s; web/index.html and the Vite config disagree", tc.name)
			}
			path := string(m[1])

			res := h.do(t, http.MethodGet, path, nil, nil, false)
			if res.code != http.StatusOK {
				t.Fatalf("GET %s = %d, want 200; the shell asks for this file and nothing serves it",
					path, res.code)
			}
			if ct := res.header.Get("Content-Type"); !strings.Contains(ct, tc.wantCT) {
				t.Errorf("GET %s answered %q, want %q", path, ct, tc.wantCT)
			}
			// Content-hashed names cannot change, so the long cache is what
			// keeps a rebuilt interface from being served stale.
			if cc := res.header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
				t.Errorf("GET %s answered Cache-Control %q, want immutable", path, cc)
			}
		})
	}
}

// The favicon, and the browser chrome that has to be in the document rather
// than in the stylesheet.
//
// The tab shows the product rather than a blank page icon, and the browser's
// own chrome is tinted to match the page rather than framing a dark interface
// in white.
//
// `color-scheme` is deliberately not on this list, though the page it replaced
// carried it as a meta tag. The stylesheet sets it from the theme attribute
// instead, which is what makes it follow the toggle — a meta tag can only say
// one thing, and the interface says two.
func TestTheShellCarriesAFaviconAndBrowserChrome(t *testing.T) {
	requireShell(t)

	h := newHarness(t, true)

	for _, want := range []string{
		`<link rel="icon" href="/admin/favicon.svg"`,
		`<meta name="theme-color"`,
		`<div id="root">`,
	} {
		if !strings.Contains(string(distShell), want) {
			t.Errorf("the shell has no %s", want)
		}
	}

	// And the icon is actually served. A link to a 404 is a blank tab with an
	// extra step in front of it.
	res := h.do(t, http.MethodGet, "/admin/favicon.svg", nil, nil, false)
	if res.code != http.StatusOK {
		t.Fatalf("GET /admin/favicon.svg = %d", res.code)
	}
	if ct := res.header.Get("Content-Type"); !strings.Contains(ct, "svg") {
		t.Errorf("Content-Type = %q; a browser will not treat it as an icon", ct)
	}
	if !strings.Contains(res.text(), "<svg") {
		t.Error("the favicon is not an SVG")
	}
}

// The stylesheet the unbuilt page borrows is served without a session.
//
// It is the last file left over from the server-rendered interface, and it is
// still served because unbuiltHandler links to it — the CSP forbids the inline
// <style> that page would otherwise need, and an unstyled page in exactly the
// situation it exists for is worse than no page. Served without a session for
// the same reason the shell is: it appears before anybody can sign in.
//
// The script that used to sit beside it is gone. It was the generated form's
// driver, and the form is the client-side interface now.
func TestTheUnbuiltPagesStylesheetIsServedWithoutASession(t *testing.T) {
	h := newHarness(t, true)

	res := h.do(t, http.MethodGet, "/admin/app.css", nil, nil, false)
	if res.code != http.StatusOK {
		t.Errorf("GET /admin/app.css = %d", res.code)
	}
	if len(res.body) == 0 {
		t.Error("GET /admin/app.css: empty")
	}
}

// The shell itself must not be cached.
//
// It is not hashed and it does change between builds: a browser holding
// yesterday's index.html asks for yesterday's bundle, which the rebuild has
// already deleted — so a stale shell is a blank page, not a stale page.
func TestTheShellIsNotCached(t *testing.T) {
	requireShell(t)

	h := newHarness(t, true)
	res := h.do(t, http.MethodGet, "/admin/", nil, nil, false)

	if res.code != http.StatusOK {
		t.Fatalf("GET /admin/ = %d: %s", res.code, res.text())
	}
	if cc := res.header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	if ct := res.header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type = %q, want HTML", ct)
	}
}

// Every page path answers with the shell, and the API answers with itself.
//
// ---------------------------------------------------------------------------
// There is one catch-all for the interface and another under /api, and which
// one answers a given path is decided by chi's rule that a static segment beats
// a wildcard. That rule is load-bearing in both directions, and neither failure
// is visible from reading the two registrations — they live in different files,
// and each looks correct on its own:
//
//	/admin/api/typo answered by the shell is an HTML page with a 200 in it, and
//	a caller parsing JSON reports "the server is broken" rather than "that
//	endpoint does not exist";
//
//	/admin/channels answered by the API's 404 is the interface failing to load
//	at the path its own sidebar links to.
//
// The interface paths below are the ones web/src/App.tsx declares, plus the two
// the server owns outright — the sign-in screen and the first-run form. A path
// this interface does not have is on the list too, because the client-side
// router renders its own 404 and the server must not get there first.
//
// The session is deliberately absent. The shell is served to nobody in
// particular: the sign-in screen is one of the paths below, so a session check
// in front of it would be a redirect to a page that also needs one.
// ---------------------------------------------------------------------------
func TestEveryPagePathAnswersWithTheShellAndTheAPIDoesNot(t *testing.T) {
	requireShell(t)

	h := newHarness(t, true)

	pages := []string{
		"/admin",
		"/admin/",
		"/admin/channels",
		"/admin/channels/new",
		"/admin/channels/oncall",
		"/admin/deliveries",
		"/admin/deliveries/abc123",
		"/admin/audit",
		"/admin/keys",
		"/admin/api-docs",
		"/admin/start",
		"/admin/password",
		"/admin/login",
		"/admin/setup",
		// Not a page this interface has. It still gets the shell, and the
		// client-side router is what decides that.
		"/admin/nothing-here",
	}

	for _, path := range pages {
		t.Run(path, func(t *testing.T) {
			res := h.do(t, http.MethodGet, path, nil, nil, false)
			if res.code != http.StatusOK {
				t.Fatalf("GET %s = %d, want the shell", path, res.code)
			}
			if !strings.Contains(res.text(), `<div id="root">`) {
				t.Errorf("GET %s did not answer with the shell", path)
			}
			if ct := res.header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
				t.Errorf("GET %s answered %q, want HTML", path, ct)
			}
		})
	}
}

// A path under /api that no route matched is a JSON 404, not the interface.
//
// The other half of the precedence above, and the one that is easy to get wrong
// in the direction nobody notices: with only a shell catch-all, /admin/api/typo
// answers 200 with a page of HTML, and every client-side fetch helper in the
// world reports a parse failure.
//
// The bare "/admin/api" is on the list because it is the one that was wrong
// first. chi keeps a wildcard's pattern as the prefix before the asterisk, so
// "/api/*" is a node for "/api/" and a request without the trailing slash never
// reaches it — which is a distinction that exists nowhere except in the router's
// own matching, and that a test written from the route table would not guess.
func TestAnUnknownAPIPathIsAnsweredAsJSON(t *testing.T) {
	requireShell(t)

	h := newHarness(t, true)

	for _, path := range []string{"/admin/api", "/admin/api/", "/admin/api/typo"} {
		t.Run(path, func(t *testing.T) {
			res := h.do(t, http.MethodGet, path, nil, nil, false)
			if res.code != http.StatusNotFound {
				t.Fatalf("GET %s = %d, want 404", path, res.code)
			}
			if ct := res.header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
				t.Errorf("GET %s answered %q, want JSON", path, ct)
			}
			if strings.Contains(res.text(), "<html") {
				t.Error("an unknown API path answered with a page")
			}

			var body errorBody
			res.decode(t, &body)
			if body.Error != "not_found" {
				t.Errorf("error = %q, want not_found", body.Error)
			}
			if body.Message == "" {
				t.Error("the 404 carries no message for the operator to read")
			}
		})
	}
}

// The copy table is readable without a session, and that is a decision.
//
// The sign-in screen is client-side, so every word on it — both field labels,
// the button, the language names — comes from this endpoint, and it is reached
// before there is a session to send. The alternative was a second, smaller copy
// table in TypeScript for the four screens that render signed out.
//
// It is asserted here rather than left to the endpoint list, because the
// tempting change is to put it back behind the session check where the rest of
// the client's endpoints live — and the symptom of doing that is a sign-in page
// whose heading says "Could not load the interface copy", which reads like a
// server fault rather than like a routing mistake.
func TestTheCopyTableIsReadableWithoutASession(t *testing.T) {
	h := newHarness(t, true)

	res := h.do(t, http.MethodGet, "/admin/api/i18n", nil, nil, false)
	if res.code != http.StatusOK {
		t.Fatalf("GET /admin/api/i18n without a session = %d, want 200", res.code)
	}

	var got i18nResponse
	res.decode(t, &got)
	if got.T == nil || got.T.LoginUsername == "" {
		t.Error("the copy table arrived without the sign-in screen's own labels")
	}
}

// Whether first-run setup is needed, answerable before there is a session.
//
// The server used to answer this with a redirect: every page handler checked
// setupRequired first and sent an unclaimed deployment to /admin/setup. That
// check is in the client now, so the client needs the fact rather than the
// redirect — and it needs it on the sign-in screen, which has no session to ask
// with. See setupStatus.
func TestSetupStatusIsReadableWithoutASession(t *testing.T) {
	t.Run("an unclaimed deployment says so", func(t *testing.T) {
		h := newUnclaimedHarness(t)

		res := h.do(t, http.MethodGet, "/admin/api/setup", nil, nil, false)
		if res.code != http.StatusOK {
			t.Fatalf("GET /admin/api/setup = %d: %s", res.code, res.text())
		}

		var got setupStatusResponse
		res.decode(t, &got)
		if !got.Required {
			t.Error("a deployment with no administrator reported that setup was done")
		}
	})

	t.Run("a claimed one says that instead", func(t *testing.T) {
		// The configured hash counts as an administrator even before anybody has
		// signed in with it, so this deployment is claimed.
		h := newHarness(t, true)

		res := h.do(t, http.MethodGet, "/admin/api/setup", nil, nil, false)
		var got setupStatusResponse
		res.decode(t, &got)
		if got.Required {
			t.Error("a deployment with a configured administrator offered first-run setup")
		}
	})
}

// The generated form has to keep the distinction a JSON client cannot see.
//
// ---------------------------------------------------------------------------
// This is the test for the reason GET /api/channel-form exists at all, and it
// is the one that would fail if somebody decided the client could build the
// form from /api/channels/types instead.
//
// requiredWhenShown says a conditional parameter is required when it applies
// unless it declares a default. The schema declares those defaults as `any`
// with omitempty, so a default of "", 0 or false does not survive the trip
// through JSON — and "absent" is exactly what the rule tests for. A client
// deriving the rule from the wire therefore marks these fields required, and
// the operator meets a form that refuses to submit until they fill in a box the
// server never wanted.
//
// The fields below are the ones that actually have such a default. If one is
// ever given a non-empty default, or loses its ShowIf, this test fails — which
// is correct: it would no longer be covering the case.
// ---------------------------------------------------------------------------
func TestChannelFormKeepsTheDefaultsAJSONClientCannotSee(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	var got channelFormData
	res := h.do(t, http.MethodGet, "/admin/api/channel-form", nil, cookie, false)
	if res.code != http.StatusOK {
		t.Fatalf("GET /admin/api/channel-form = %d: %s", res.code, res.text())
	}
	res.decode(t, &got)

	if len(got.Forms) == 0 {
		t.Fatal("no forms in the response")
	}
	if got.Editing != "" {
		t.Errorf("editing = %q, want empty for a create", got.Editing)
	}

	byName := map[string]fieldView{}
	for _, f := range got.Forms {
		for _, field := range f.Fields {
			byName[f.Type+"."+field.Name] = field
		}
	}

	cases := []struct {
		key string
		why string
	}{
		{
			key: "webhook.signature_prefix",
			why: "defaults to the empty string, which omitempty drops from the schema",
		},
		{
			key: "webhook.signature_header",
			why: "defaults to X-Signature",
		},
		{
			key: "wecom.to_user",
			why: "defaults to the empty string",
		},
	}

	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			field, ok := byName[tc.key]
			if !ok {
				t.Fatalf("%s is not in the generated form", tc.key)
			}
			if field.Required {
				t.Errorf("%s came back required; it %s, so the server does not want it",
					tc.key, tc.why)
			}
		})
	}

	// And the other half: a conditional field with no default IS required when
	// it applies. Without this, a bug that marked everything optional would
	// pass the assertions above.
	secret, ok := byName["webhook.secret"]
	if !ok {
		t.Fatal("webhook.secret is not in the generated form")
	}
	if !secret.Required {
		t.Error("webhook.secret came back optional; it is required when auth_type is hmac")
	}
	if secret.ShowIfField != "auth_type" || secret.ShowIfEquals != "hmac" {
		t.Errorf("webhook.secret: show_if = %q/%q, want auth_type/hmac",
			secret.ShowIfField, secret.ShowIfEquals)
	}
}

// A private parameter is never rendered, whatever is stored.
//
// The masked configuration is the caller's job and buildField does it again
// anyway, so that a future caller which forgets cannot turn this endpoint into
// a way of reading credentials out of the database.
func TestChannelFormNeverSendsAPrivateValue(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	// A channel with a credential in it.
	save := h.do(t, http.MethodPost, "/admin/api/channels", saveRequest{
		Name:    "with-a-secret",
		Type:    "webhook",
		Config:  map[string]any{"url": "https://example.com/hook", "auth_type": "hmac", "secret": "hunter2"},
		Editing: newChannel,
	}, cookie, true)
	if save.code != http.StatusOK {
		t.Fatalf("saving the channel failed: %d: %s", save.code, save.text())
	}

	var got channelFormData
	res := h.do(t, http.MethodGet, "/admin/api/channel-form?name=with-a-secret", nil, cookie, false)
	res.decode(t, &got)

	if got.Editing != "with-a-secret" {
		t.Fatalf("editing = %q", got.Editing)
	}
	if !got.EditSecrets["secret"] {
		t.Error("edit_secrets does not report the stored credential")
	}

	for _, f := range got.Forms {
		for _, field := range f.Fields {
			if field.Private && field.Value != "" {
				t.Errorf("%s.%s carries a value for a private parameter", f.Type, field.Name)
			}
		}
	}

	// The raw body too, in case the value is somewhere this walk does not look.
	if strings.Contains(res.text(), "hunter2") {
		t.Error("the response body contains the stored credential")
	}
}

// The endpoints the interface reads after signing in have to be mounted, behind
// a session, and answering JSON.
//
// They are registered in a different file from the rest of the admin's routes,
// which is exactly how an endpoint ends up existing and never being reachable.
// The session check is asserted as hard as the reachability: both of these
// describe the deployment — one lists every endpoint this service has, the
// other is a checklist of what it contains — and neither is for a stranger.
//
// The copy table is deliberately not in this list. It is public, and
// TestTheCopyTableIsReadableWithoutASession says why.
func TestClientInterfaceEndpoints(t *testing.T) {
	paths := []string{"/admin/api/onboarding", "/admin/api/api-docs"}

	h := newHarness(t, true)
	cookie := h.signIn(t)

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			res := h.do(t, http.MethodGet, path, nil, cookie, false)
			if res.code != http.StatusOK {
				t.Fatalf("GET %s = %d: %s", path, res.code, res.text())
			}
			if ct := res.header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
				t.Errorf("GET %s answered %q, want JSON", path, ct)
			}

			// Signed out, the same path must refuse. A 200 here would mean the
			// endpoint was mounted outside the session group.
			anon := h.do(t, http.MethodGet, path, nil, nil, false)
			if anon.code != http.StatusUnauthorized {
				t.Errorf("GET %s without a session = %d, want 401", path, anon.code)
			}
		})
	}
}

// The copy table the client renders from has to be the same table the templates
// render from, or the two interfaces say different things about the same button.
func TestI18nEndpointServesTheTable(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	var got i18nResponse
	res := h.do(t, http.MethodGet, "/admin/api/i18n?lang=en", nil, cookie, false)
	if res.code != http.StatusOK {
		t.Fatalf("status = %d: %s", res.code, res.text())
	}
	res.decode(t, &got)

	if got.Lang != string(i18n.EN) {
		t.Errorf("lang = %q, want %q", got.Lang, i18n.EN)
	}
	if got.T == nil {
		t.Fatal("no copy table in the response")
	}
	if want := i18n.For(i18n.EN).TitleChannels; got.T.TitleChannels != want {
		t.Errorf("TitleChannels = %q, want %q", got.T.TitleChannels, want)
	}
	if len(got.Langs) != len(i18n.Langs) {
		t.Errorf("got %d languages, want %d", len(got.Langs), len(i18n.Langs))
	}
}

// The channel parameter copy has to travel with the table, because the
// client-side form generates its labels from the same schema the server does
// and has no other way to reach the translation.
//
// Chinese has a table and English must not: the English label is the schema's
// own declaration, and a second copy of it here would be a second place to
// update. A non-empty Params for English would mean somebody had started
// maintaining that duplicate.
func TestI18nEndpointServesParameterCopy(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	t.Run("chinese has a table", func(t *testing.T) {
		var got i18nResponse
		res := h.do(t, http.MethodGet, "/admin/api/i18n?lang=zh", nil, cookie, false)
		res.decode(t, &got)

		want := i18n.Params(i18n.ZH)
		if len(got.Params) != len(want) {
			t.Errorf("got %d parameter translations, want %d", len(got.Params), len(want))
		}

		// One key checked field by field, because a count only proves the map
		// arrived. A copy that lost its description in the round trip would
		// still be a map of the right size, and the form would show a label
		// with nothing under it.
		const key = "email.host"
		gotCopy, ok := got.Params[key]
		if !ok {
			t.Fatalf("%s missing from the response", key)
		}
		if gotCopy.Label != want[key].Label || gotCopy.Desc != want[key].Desc {
			t.Errorf("%s: got %+v, want %+v", key, gotCopy, want[key])
		}
		if gotCopy.Label == "" {
			t.Error("email.host has an empty label")
		}
	})

	t.Run("english has none", func(t *testing.T) {
		var got i18nResponse
		res := h.do(t, http.MethodGet, "/admin/api/i18n?lang=en", nil, cookie, false)
		res.decode(t, &got)

		if len(got.Params) != 0 {
			t.Errorf("English carries %d parameter translations; the schema is the English",
				len(got.Params))
		}
	})
}
