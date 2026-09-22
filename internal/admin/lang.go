package admin

import (
	"context"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/go-chi/chi/v5"

	"notifyrelay/internal/admin/i18n"
)

// Language resolution.
//
// Three sources, in this order: the query parameter, then the cookie, then the
// default. The parameter wins so a link can pin a language — a screenshot, a
// bug report, a bookmark — and the cookie is what makes the choice survive
// navigation. Keeping the choice in the query string alone was the previous
// implementation's mistake: the first link the operator clicked put them back
// in English, so the switch looked broken in the one way that is hard to
// report, because it worked right up until it mattered.

// langKey carries the request's resolved language.
type langKey struct{}

// withLang resolves the language once and puts it in the request context.
//
// Once, rather than at each of the three places that need it — the page data,
// the template set and the script's string table — because three independent
// resolutions are three chances to disagree, and the disagreement would be a
// page whose chrome is in one language and whose body is in another.
func withLang(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), langKey{}, langOf(r))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// langFrom reads the language resolved by withLang.
//
// Falls back to the default rather than panicking: a handler reached without
// the middleware is a wiring mistake, and serving the default language is a
// better answer to it than a 500 on every page.
func langFrom(ctx context.Context) i18n.Lang {
	if l, ok := ctx.Value(langKey{}).(i18n.Lang); ok {
		return l
	}
	return i18n.Default
}

// copyFor is the copy table for a request.
//
// The short name for what is otherwise i18n.For(langFrom(r.Context())) at
// forty-odd call sites, most of which are one error message. Handlers use it
// directly rather than passing a language around: a handler that has the
// request has everything it needs, and threading a language through would be
// threading the same value twice.
func copyFor(r *http.Request) *i18n.Messages {
	return i18n.For(langFrom(r.Context()))
}

// langOf resolves a request's language from the three sources.
func langOf(r *http.Request) i18n.Lang {
	if tag := r.URL.Query().Get(i18n.Param); tag != "" {
		return i18n.Parse(tag)
	}
	if c, err := r.Cookie(i18n.CookieName); err == nil {
		return i18n.Parse(c.Value)
	}
	return i18n.Default
}

// setLanguage implements GET /admin/lang/{lang}.
//
// A redirect rather than a page of its own, because the point is to change the
// language of the page the reader is already looking at. Registered outside the
// session check, because the sign-in page and the first-run page both need a
// switcher and neither has a session yet — which is also why the choice is a
// cookie rather than a field on the session.
func (h *handler) setLanguage(w http.ResponseWriter, r *http.Request) {
	lang := i18n.Parse(chi.URLParam(r, "lang"))

	http.SetCookie(w, &http.Cookie{
		Name:     i18n.CookieName,
		Value:    string(lang),
		Path:     "/admin",
		HttpOnly: true,
		Secure:   cookieSecure(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   i18n.CookieMaxAge,
	})

	http.Redirect(w, r, returnPath(r), http.StatusFound)
}

// returnPath is where the language switch sends the reader back to.
//
// Checked rather than trusted. A redirect target taken from the query string is
// an open redirect, and "the admin interface bounced me to another site" is a
// phishing page with the operator's trust already earned. A backslash is
// refused along with everything else outside this interface: browsers read
// "/\evil.example" as a protocol-relative URL, which is the trick that gets
// past a check that only asks whether the value starts with a slash.
func returnPath(r *http.Request) string {
	const fallback = "/admin/"

	to := r.URL.Query().Get("to")
	if to == "" || strings.ContainsAny(to, `\`+"\r\n\t") {
		return fallback
	}

	target, raw, _ := strings.Cut(to, "?")

	// The path is cleaned before it is checked, not after. "/admin/../etc/passwd"
	// passes a prefix test and a browser resolves it to "/etc/passwd" — so the
	// check has to be against what the browser will do with the value, not
	// against the value as it was written.
	target = path.Clean(target)
	if target != "/admin" && !strings.HasPrefix(target, "/admin/") {
		return fallback
	}

	// A target that still carries the language parameter would undo the choice
	// on the very next request, which reads as "the switch does not stick".
	return target + stripLangParam(raw)
}

// stripLangParam removes the language override from a query string, returning
// it with its leading "?" or as the empty string.
func stripLangParam(raw string) string {
	if raw == "" {
		return ""
	}

	values, err := url.ParseQuery(raw)
	if err != nil {
		// An unparseable query is dropped rather than passed on: this is a
		// redirect target, and the only thing this function does is make it
		// smaller.
		return ""
	}

	values.Del(i18n.Param)
	if len(values) == 0 {
		return ""
	}
	return "?" + values.Encode()
}

// langOption is one entry in the switcher.
type langOption struct {
	Tag   string
	Label string
	Href  string
	On    bool
}

// langOptions builds the switcher for the page being rendered.
//
// The link points back at the page the reader is on, so choosing a language
// does not also navigate them somewhere else — the two are separate decisions
// and coupling them is how a reader loses their place.
//
// The current URL is rebuilt from the request rather than taken from a Referer
// header: that header is absent on a first visit and attacker-controlled on
// every other one.
func langOptions(r *http.Request, current i18n.Lang) []langOption {
	to := path.Clean(r.URL.Path) + stripLangParam(r.URL.RawQuery)

	out := make([]langOption, 0, len(i18n.Langs))
	for _, l := range i18n.Langs {
		out = append(out, langOption{
			Tag:   string(l),
			Label: l.Name(),
			Href:  "/admin/lang/" + string(l) + "?to=" + url.QueryEscape(to),
			On:    l == current,
		})
	}
	return out
}
