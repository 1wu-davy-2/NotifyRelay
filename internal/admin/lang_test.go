package admin

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"notifyrelay/internal/admin/i18n"
)

// raw sends a GET and hands back the recorder, for the tests that need a
// response header the harness does not collect — a redirect's destination and
// the language cookie both live there.
func (h *harness) raw(t *testing.T, path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, path, nil)
	for _, c := range cookies {
		if c != nil {
			req.AddCookie(c)
		}
	}
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	return rec
}

// langCookie returns the language cookie a response set, if it set one.
func langCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == i18n.CookieName {
			return c
		}
	}
	return nil
}

// shellPage fetches the shell in a named language, with any cookies a test
// needs to carry.
//
// The language is passed as a query parameter rather than through the cookie,
// which is what makes these tests independent of each other — and it exercises
// the precedence rule every time, since the parameter is the one source that
// beats the others.
func (h *harness) shellPage(t *testing.T, path string, lang i18n.Lang, cookies ...*http.Cookie) string {
	t.Helper()
	requireShell(t)

	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	path += sep + i18n.Param + "=" + string(lang)

	rec := h.raw(t, path, cookies...)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: status %d: %s", path, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// langAttr reads the lang attribute off the document's opening tag.
//
// The value and not a substring of the tag. The tag the server writes carries
// the theme beside the language — `<html lang="zh-CN" data-theme="dark">` — so
// an assertion looking for the whole of `<html lang="zh-CN">` is an assertion
// about the attribute order, and it fails the day the theme is written first.
// The server-rendered pages this replaced had one attribute on that tag, which
// is why the older version of these tests got away with it.
func langAttr(t *testing.T, shell string) string {
	t.Helper()

	tag := headOf(t, []byte(shell))
	m := htmlLang.FindStringSubmatch(tag)
	if m == nil {
		t.Fatalf("the shell's opening tag carries no lang attribute: %s", tag)
	}
	return m[1]
}

var htmlLang = regexp.MustCompile(`\slang="([^"]*)"`)

// The interface is Chinese by default, which is the change that prompted all of
// this. Everything else here is about the switch working.
//
// Both halves are checked, because they are written in different languages —
// the markup's `lang` attribute by the Go that serves the shell, the words by
// the copy table — and either one alone would look correct while the page
// announced Chinese content in an English voice.
func TestLanguage_DefaultsToChinese(t *testing.T) {
	h := newHarness(t, true)

	if got := langAttr(t, h.shellPage(t, "/admin/channels", i18n.ZH)); got != "zh-CN" {
		t.Errorf("the shell is marked %q, want zh-CN", got)
	}

	// The brand is the product's Chinese name, not the repository's.
	if got := i18n.For(i18n.Default).AppName; got != "信使中枢" {
		t.Errorf("the default brand is %q, want the Chinese name", got)
	}
	if got := i18n.For(i18n.Default).NavChannels; got != "渠道" {
		t.Errorf("the default navigation is %q, want Chinese", got)
	}
}

// The switch sets a cookie and returns the reader to the page they were on.
// Sending them somewhere else would make choosing a language also a navigation,
// and the two are separate decisions.
func TestLanguage_SwitchSetsACookieAndReturnsToThePage(t *testing.T) {
	h := newHarness(t, true)

	target := "/admin/channels?status=failed"
	rec := h.raw(t, "/admin/lang/en?to="+url.QueryEscape(target))

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want a redirect", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != target {
		t.Errorf("Location = %q, want %q", got, target)
	}

	c := langCookie(t, rec)
	if c == nil {
		t.Fatal("no language cookie was set")
	}
	if c.Value != "en" {
		t.Errorf("cookie value = %q, want en", c.Value)
	}
	if !c.HttpOnly {
		t.Error("the language cookie is readable from JavaScript")
	}
	if c.Path != "/admin" {
		t.Errorf("Path = %q, want /admin", c.Path)
	}
	if c.MaxAge <= 0 {
		t.Errorf("MaxAge = %d; a session cookie loses the choice when the browser closes", c.MaxAge)
	}
}

// The choice has to survive the next link the operator clicks. Keeping it in
// the query string alone was the previous implementation's mistake: it worked
// until the first navigation, which is the worst way for a feature to fail.
//
// The cookie is read by the server as it serves the shell, which writes the
// language into the `lang` attribute — so this is a test about a cookie
// surviving a navigation *and* about the server acting on it before the bundle
// runs, which is what keeps the page from flashing the wrong language.
func TestLanguage_TheChoiceSurvivesNavigation(t *testing.T) {
	h := newHarness(t, true)

	rec := h.raw(t, "/admin/lang/en")
	lang := langCookie(t, rec)
	if lang == nil {
		t.Fatal("no language cookie")
	}

	// The cookie alone, with no language parameter on the URL — which is the
	// whole point of the test, since the parameter is the source that beats it.
	for _, path := range []string{"/admin/channels", "/admin/deliveries", "/admin/keys", "/admin/login"} {
		rec := h.raw(t, path, lang)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: status %d: %s", path, rec.Code, rec.Body.String())
			continue
		}
		if got := langAttr(t, rec.Body.String()); got != "en" {
			t.Errorf("%s came back marked %q, want en", path, got)
		}
	}
}

// A link can pin a language for one request without changing the stored choice.
// That is what makes a screenshot or a bug report reproducible.
func TestLanguage_TheQueryParameterBeatsTheCookie(t *testing.T) {
	h := newHarness(t, true)

	english := &http.Cookie{Name: i18n.CookieName, Value: "en"}

	if got := langAttr(t, h.shellPage(t, "/admin/channels", i18n.ZH, english)); got != "zh-CN" {
		t.Errorf("the shell is marked %q; the query parameter did not override the cookie", got)
	}

	// And it did not rewrite the stored choice: the next request without the
	// parameter is back to what the cookie says.
	if got := langAttr(t, h.shellPage(t, "/admin/channels", i18n.EN, english)); got != "en" {
		t.Errorf("the shell is marked %q; the query parameter overwrote the cookie", got)
	}
}

// An unrecognised language falls back rather than failing. A 400 on a page
// whose whole purpose is to be readable is the wrong answer to "we do not have
// your language".
func TestLanguage_AnUnknownTagFallsBack(t *testing.T) {
	h := newHarness(t, true)

	for _, tag := range []string{"de", "xx", "", "../../etc/passwd", "zh-Hant"} {
		rec := h.raw(t, "/admin/channels?lang="+url.QueryEscape(tag))
		if rec.Code != http.StatusOK {
			t.Errorf("lang=%q: status %d, want a page rather than a refusal", tag, rec.Code)
			continue
		}
		if got := langAttr(t, rec.Body.String()); got != "zh-CN" {
			t.Errorf("lang=%q fell back to %q, want the default", tag, got)
		}
	}
}

// The redirect target is checked, not trusted.
//
// A target taken from the query string is an open redirect, and "the admin
// interface sent me to another site" is a phishing page with the operator's
// trust already earned. A backslash is on the list because browsers read
// "/\evil.example" as a protocol-relative URL, which is the trick that gets
// past a check that only asks whether the value starts with a slash.
func TestLanguage_RefusesToRedirectOffSite(t *testing.T) {
	h := newHarness(t, true)

	for _, to := range []string{
		"https://evil.example/phish",
		"//evil.example/phish",
		`/\evil.example/phish`,
		`/admin/../etc/passwd`,
		"/etc/passwd",
		"/admin-evil/",
		"javascript:alert(1)",
		"/admin/x\r\nLocation: https://evil.example",
		"",
	} {
		rec := h.raw(t, "/admin/lang/en?to="+url.QueryEscape(to))
		if got := rec.Header().Get("Location"); got != "/admin/" {
			t.Errorf("to=%q redirected to %q, want the fallback", to, got)
		}
	}
}

// A target that still carries the language parameter would undo the choice on
// the very next request, which reads as "the switch does not stick".
func TestLanguage_TheTargetLosesItsOwnLanguageParameter(t *testing.T) {
	h := newHarness(t, true)

	rec := h.raw(t, "/admin/lang/en?to="+url.QueryEscape("/admin/deliveries?lang=zh&status=failed"))
	if got, want := rec.Header().Get("Location"), "/admin/deliveries?status=failed"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

// The switch is reachable without a session, because the sign-in screen is the
// screen that needs it most and it has no session to offer.
//
// The other half of this test used to assert that the switcher was rendered
// into the sign-in and first-run pages. It is rendered by the client-side
// interface now — into the card the two signed-out screens share — so what the
// server owes is the cookie, and the path it returns the reader to is checked
// above.
func TestLanguage_SwitchWorksWithoutASession(t *testing.T) {
	h := newHarness(t, true)

	rec := h.raw(t, "/admin/lang/en?to=/admin/login")
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d without a session, want a redirect", rec.Code)
	}
	if langCookie(t, rec) == nil {
		t.Error("the switch set no cookie without a session")
	}

	// And the destination it returns to is served without one, or the switch
	// would set a cookie and then bounce the reader somewhere they cannot go.
	if res := h.do(t, http.MethodGet, "/admin/login", nil, nil, false); res.code != http.StatusOK {
		t.Errorf("GET /admin/login without a session = %d, want the shell", res.code)
	}
}

// The html element's lang attribute is what a screen reader pronounces and what
// the browser picks a font from. It was hardcoded to "en".
//
// It is written by the server as it serves the shell, rather than set by the
// bundle after it loads — the CSP forbids the inline script that would normally
// do this before the first paint, so an English interface would otherwise be
// announced in Chinese for as long as it took React to mount.
func TestEveryPage_DeclaresItsLanguage(t *testing.T) {
	h := newHarness(t, true)

	for _, tc := range []struct{ lang, want string }{
		{"zh", "zh-CN"},
		{"en", "en"},
	} {
		for _, path := range []string{"/admin/channels", "/admin/deliveries", "/admin/login", "/admin/setup"} {
			if got := langAttr(t, h.shellPage(t, path, i18n.Lang(tc.lang))); got != tc.want {
				t.Errorf("%s (%s): lang = %q, want %q", path, tc.lang, got, tc.want)
			}
		}
	}
}
