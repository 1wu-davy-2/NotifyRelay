package admin

import (
	"io/fs"
	"log/slog"
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"

	"notifyrelay/internal/admin/i18n"
	"notifyrelay/web"
)

// The built frontend, resolved once at startup.
//
// Read eagerly rather than per request so that a broken build is a failure to
// start, which is a great deal easier to notice than a 500 on a page nobody
// opened. The same argument the template sets are parsed under.
//
// distMissing is not a fault. A backend-only checkout has no node toolchain and
// no reason to run one, and `go build` has to keep working there — so the
// absence of a build is a page that says how to make one, not a refusal to
// serve the API that build would talk to.
var (
	distFS      fs.FS
	distShell   []byte
	distMissing bool
)

func init() {
	sub, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		distMissing = true
		return
	}

	shell, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		// The placeholder in web/dist is the only file in an unbuilt checkout.
		// Reaching here is the expected state, not an error worth logging.
		distMissing = true
		return
	}

	distFS = sub
	distShell = shell
}

// registerSPA mounts the built frontend, at every page path under /admin.
//
// ---------------------------------------------------------------------------
// Two route groups, and the split between them is the whole design.
//
//	/assets/*   the content-hashed bundle. Served without a session, because it
//	            contains no data — the same argument the stylesheet and the
//	            favicon are already served under. A sign-in page that cannot
//	            load its JavaScript is a blank page.
//
//	/*          the shell, for every other GET. Also served without a session,
//	            and this one is a choice rather than an inheritance: the shell
//	            is an empty div and a script tag, and putting a session check
//	            in front of it would produce a redirect chain to /admin/login
//	            for an operator whose cookie has merely expired. The bundle
//	            asks /admin/api/session instead, gets a 401, and routes to the
//	            sign-in screen — which is one round trip rather than two, and
//	            lands in the same place.
//
// The catch-all is what makes this the interface rather than a page inside
// one: /admin/deliveries/abc is not a file and never was, and a refresh on it
// has to work. The paths the client-side router understands are listed in
// web/src/App.tsx; a path it does not recognise is its own 404 screen rather
// than this router's, which is why nothing here enumerates them.
//
// Two things are registered after this and both win over it, because chi
// matches a static segment before a wildcard: the JSON 404 that keeps
// /admin/api/typo from answering with HTML, and the session-checked API
// routes themselves.
// ---------------------------------------------------------------------------
func registerSPA(r chi.Router) {
	if distMissing {
		// One page, at every path, saying what to run. Serving a 404 here would
		// be technically correct and would send somebody looking for a routing
		// bug that is not there.
		r.Get("/", unbuiltHandler)
		r.Get("/*", unbuiltHandler)
		return
	}

	assets, err := fs.Sub(distFS, "assets")
	if err != nil {
		// No assets directory: the shell would load and then fail to find its
		// bundle, which is a blank page and a console error. Reported at
		// startup for the same reason as the others.
		slog.Error("admin: the built frontend has no assets directory",
			slog.String("error", err.Error()))
		r.Get("/", unbuiltHandler)
		r.Get("/*", unbuiltHandler)
		return
	}

	// The file name comes from chi's wildcard rather than from the request
	// path, and that is not a style choice.
	//
	// The obvious version is http.StripPrefix("/assets/", http.FileServer(…)),
	// and it 404s. This router is mounted at /admin, and chi's Mount rewrites
	// the *route path* it matches against while leaving r.URL.Path alone — so
	// StripPrefix looks for a prefix that is not there, strips nothing, and the
	// file server goes looking for a file called "admin/assets/index-….js".
	// The same trap the note above registerAssets describes, from the other
	// side.
	//
	// chi.URLParam(r, "*") is what the router actually matched, which is the
	// name relative to this route and therefore the name relative to the
	// embedded directory. http.ServeFileFS handles the rest: content type from
	// the extension, range requests, and a refusal to serve a path with ".."
	// in it.
	r.Get("/assets/*", func(w http.ResponseWriter, req *http.Request) {
		// Vite writes a content hash into every asset filename, so a given
		// URL's bytes cannot change. That makes the long cache safe, and it is
		// what keeps a rebuilt interface from being served from a browser cache
		// that has no way to know it is stale.
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		http.ServeFileFS(w, req, assets, chi.URLParam(req, "*"))
	})

	shell := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The shell is not hashed and does change between builds, so it must
		// not be cached: a browser holding yesterday's index.html requests
		// yesterday's bundle, which the rebuild has already deleted.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(applyPreferences(distShell, r))
	})

	// Both spellings, because the mount at /admin means "/admin" and "/admin/"
	// arrive as "" and "/" depending on how chi rewrites them, and a shell
	// served at one but not the other is a redirect nobody asked for.
	r.Get("/", shell)
	r.Get("/*", shell)
}

// themeCookie is where the interface keeps the light/dark choice. The name is
// duplicated in web/src/lib/theme.ts, which is the only writer.
const themeCookie = "nr_theme"

// shellHead matches the document's opening <html> tag, anchored to the start of
// a line.
//
// ---------------------------------------------------------------------------
// Anchoring to the line and replacing the whole tag, rather than replacing the
// attribute strings, is not a style preference — it is the fix for a bug this
// had on its first version.
//
// That version did `bytes.Replace(shell, []byte(`lang="zh-CN"`), …)` with a
// count of one. index.html explains its own design in a comment above the tag,
// and the comment quotes the attribute. The comment comes first, so the
// replacement rewrote the prose and left the real tag in Chinese — and the page
// still loaded, in the wrong language, announcing English content in a Chinese
// voice, with nothing anywhere reporting a problem. The test that covers this
// passed for the theme only because `data-theme="dark"` happened to appear
// exactly once.
//
// Matching the tag itself removes the class of bug: there is one <html> tag in
// the document and it is the first line that starts with one. The `(?m)^`
// anchor is what keeps a tag mentioned inside a comment — which is indented, as
// prose is — from being the one that matches.
// ---------------------------------------------------------------------------
var shellHead = regexp.MustCompile(`(?m)^<html\b[^>]*>`)

// applyPreferences fills in the two attributes that depend on the request.
//
// ---------------------------------------------------------------------------
// Why the server does this at all, rather than a script in the page.
//
// The normal answer is an inline script that sets the attribute before the
// first paint. The CSP here is `script-src 'self'` with no 'unsafe-inline', so
// that script is exactly what the page forbids — and without it, a saved light
// theme means a frame of dark before the bundle runs.
//
// Both values are known when the request arrives. The theme is in a cookie the
// interface writes; the language is resolved by withLang from the same cookie
// every server-rendered page reads. Writing them into the markup means the
// browser's first paint is already correct, with nothing deferred and nothing
// hidden while it loads.
//
// `lang` matters for a second reason: it is what tells a screen reader which
// voice to use. Hardcoding zh-CN meant an English interface announced in
// Chinese, which is a worse failure than the flash and a quieter one.
//
// ReplaceAllFunc builds a new slice, so distShell is never mutated and two
// requests in different languages cannot race each other into the same buffer.
// ---------------------------------------------------------------------------
func applyPreferences(shell []byte, r *http.Request) []byte {
	lang := "zh-CN"
	if langFrom(r.Context()) == i18n.EN {
		lang = "en"
	}

	tag := []byte(`<html lang="` + lang + `" data-theme="` + themeOf(r) + `">`)

	return shellHead.ReplaceAllFunc(shell, func([]byte) []byte { return tag })
}

// themeOf reads the interface's theme choice, defaulting to light.
//
// Anything unrecognised is the default rather than an error, for the same
// reason i18n.Parse falls back: a preference the server does not understand is
// not a failure, and the alternative is a 400 on a page whose whole purpose is
// to be looked at.
//
// The default is written here as well as in web/src/lib/theme.ts and in
// index.html, and all three have to move together: this one is what the server
// puts in the markup for every page it serves, index.html is what Vite serves
// in a dev checkout with no Go process in front of it, and theme.ts is what a
// browser with no cookie falls back to once the bundle runs.
func themeOf(r *http.Request) string {
	c, err := r.Cookie(themeCookie)
	if err != nil {
		return "light"
	}
	if c.Value == "dark" {
		return "dark"
	}
	return "light"
}

// unbuiltHandler is what a checkout without `npm run build` serves.
//
// ---------------------------------------------------------------------------
// It covers every page path now, the sign-in form included, and that is the
// one consequence of the client-side interface worth stating plainly: an
// operator on a binary built without a frontend cannot sign in through a
// browser at all. Before the switch they could, because the sign-in form was a
// template.
//
// That is the trade, and it is the right way round for this service. The
// interface is compiled into the binary rather than deployed beside it, so a
// build without one is not a degraded deployment — it is a developer's
// checkout, and the person looking at this page is the person who can run the
// two commands below. What they get is a page that says exactly that, which is
// better than a sign-in form that works and a channel page that does not.
//
// The JSON API is genuinely unaffected: it is mounted outside this and answers
// whether or not a frontend was built.
// ---------------------------------------------------------------------------
//
// It borrows the admin's own stylesheet rather than carrying a <style> block.
// The CSP is `style-src 'self'` with no 'unsafe-inline', so an inline style
// element is blocked — which would have rendered this page as unstyled black
// text on white, in exactly the situation it exists for. The stylesheet at
// /admin/app.css is served unconditionally by registerAssets, so the page looks
// like part of the interface rather than like a browser error.
//
// The language is English and not the request's: this is read by whoever is
// building the project, which is a developer and not the operator the copy
// table exists for. Translating it would mean three fields of table for a page
// that appears only in a checkout.
func unbuiltHandler(w http.ResponseWriter, _ *http.Request) {
	const page = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Frontend not built</title>
<link rel="stylesheet" href="/admin/app.css">
</head>
<body>
<main>
<div class="page-head"><h1>Frontend not built</h1></div>
<div class="card">
<div class="empty">
<p>The operator interface is a build artefact. This binary was compiled without
one, so there is nothing under <code>/admin</code> to serve.</p>
<p>Build it, then rebuild the binary:</p>
<pre><code>cd web &amp;&amp; npm install &amp;&amp; npm run build
go build ./...</code></pre>
<p>The JSON API under <code>/api/v1</code> is unaffected and answers as usual.</p>
</div>
</div>
</main>
</body>
</html>`

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(page))
}

// ------------------------------------------------------------------- i18n

// langView is one entry in the language switcher.
//
// No href: the link has to return the reader to the page they are on, and only
// the client knows what that is. Composing it here would mean guessing from the
// API path, which is not the page.
type langView struct {
	Tag   string `json:"tag"`
	Label string `json:"label"`
	On    bool   `json:"on"`
}

// i18nResponse is the copy table, as the interface reads it.
type i18nResponse struct {
	Lang  string     `json:"lang"`
	Langs []langView `json:"langs"`
	// T is the whole message table for the resolved language.
	//
	// The whole table rather than the subset a page needs, because the page is
	// client-side and the router can reach any of them without a round trip. It
	// is around thirty kilobytes, once per session, gzipped on the wire — which
	// is cheaper than the alternative, which is a second copy of four hundred
	// strings in TypeScript that drifts from this one the first time somebody
	// edits a sentence.
	T *i18n.Messages `json:"t"`

	// Params is the channel parameter copy, keyed "<channel type>.<param>".
	//
	// Here rather than merged into the catalogue at /api/channels/types, and
	// the split is deliberate: that endpoint describes what the *server*
	// accepts and is language-independent, while this is a translation of a
	// label that already exists in English in the schema. Merging them would
	// mean the catalogue answered differently depending on a cookie, which is
	// the sort of thing that makes a cache wrong once and then forever.
	//
	// Absent for English, and that is not a gap: the English is the schema's
	// own Label and Desc, which is the declaration rather than a copy of it.
	// See i18n.Params.
	Params map[string]i18n.ParamCopy `json:"params,omitempty"`
}

// i18nJSON implements GET /admin/api/i18n.
//
// The language is resolved the same way every page resolves it — query
// parameter, then cookie, then default — so a client that passes ?lang=en gets
// English without changing what the next page will be. That is what lets the
// switcher show a preview without committing to it.
func (h *handler) i18nJSON(w http.ResponseWriter, r *http.Request) {
	lang := langFrom(r.Context())

	langs := make([]langView, 0, len(i18n.Langs))
	for _, l := range i18n.Langs {
		langs = append(langs, langView{Tag: string(l), Label: l.Name(), On: l == lang})
	}

	writeJSON(w, http.StatusOK, i18nResponse{
		Lang:   string(lang),
		Langs:  langs,
		T:      i18n.For(lang),
		Params: i18n.Params(lang),
	})
}
