// Package admin is the operator surface: the API the management UI is built on.
//
// It is a separate package from internal/api because it is a separate trust
// boundary. The notification API is authenticated by a bearer key that a
// service holds and uses to send messages; this one is authenticated by a
// session that a person holds and uses to reconfigure where those messages go.
// Sharing a package would make it easy to add a route to the wrong one.
package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"notifyrelay/internal/auth"
	"notifyrelay/internal/breaker"
	"notifyrelay/internal/config"
	"notifyrelay/internal/router"
	"notifyrelay/internal/store"
)

// csrfHeader is required on every state-changing request.
//
// SameSite=Lax already stops a cross-site POST from carrying the session
// cookie, and this is the second lock on the same door: a cross-origin script
// cannot set a custom header without a CORS preflight, and this server never
// answers one. Two independent defences, neither of which is a token the UI has
// to store and rotate.
const csrfHeader = "X-NotifyRelay-Admin"

// Deps is what the operator surface needs.
type Deps struct {
	// Config carries the operator's username, password hash and session TTL.
	//
	// The password hash may be empty, which is the first-run state: the service
	// serves the create-an-administrator page instead of the sign-in form until
	// an account exists.
	Config config.AdminConfig
	// Auth is the producer-credential configuration. Only the key names are
	// read, so the API key page can list the ones that live in the file
	// alongside the ones in the database — a list that showed only half of them
	// would be wrong in the direction that matters.
	Auth config.AuthConfig
	// Credentials holds the administrator created through the first-run page.
	// Optional; without it the configured password hash is the only way in, and
	// the setup page is not offered.
	Credentials store.AdminCredentials
	// Keys holds the API keys created through the operator surface. Optional;
	// without it the keys named in the configuration file are the only ones
	// that authenticate.
	Keys store.APIKeys
	// Channels reads and writes the stored channel instances.
	Channels *config.ChannelSource
	// Breakers is reset on request. Optional.
	Breakers *breaker.Manager
	// Router is reloaded after a configuration change. Optional, and without it
	// a saved channel does not take effect until a restart.
	Router *router.Router
	// Deliveries reads delivery state and replays dead letters. Optional.
	Deliveries DeliveryStore
	// Bodies reports whether a message is still on disk. Optional; without it
	// nothing is offered for replay, which is the honest answer when the
	// surface cannot tell.
	Bodies BodyStore
	// Queue accepts a test notification. Optional; without it the operator
	// surface cannot send one, and says so rather than offering a button that
	// does nothing.
	//
	// This is the only way into the queue from here, and it is deliberately
	// narrow: see sendTestNotification for why it takes no target.
	Queue Enqueuer
	// Waker nudges the queue after a replay. Optional.
	Waker Waker
	// Audit records what operators did. Optional.
	Audit store.AdminAudit
	Log   *slog.Logger
}

type handler struct {
	deps     Deps
	log      *slog.Logger
	password auth.PasswordHash
	sessions *sessions
	limiter  *loginLimiter
}

// NewHandler builds the operator API.
//
// It returns nil when the admin surface is disabled, so a caller can pass the
// result straight through without branching.
func NewHandler(d Deps) http.Handler {
	if !d.Config.Enabled {
		return nil
	}

	log := d.Log
	if log == nil {
		log = slog.Default()
	}

	// An empty hash is the first-run state, not a fault: the setup page creates
	// the account. Only a hash that was written down and cannot be parsed is
	// worth an error, and it must not degrade into "start anyway without a
	// password check".
	var hash auth.PasswordHash
	if strings.TrimSpace(d.Config.PasswordHash) != "" {
		parsed, err := auth.ParsePasswordHash(d.Config.PasswordHash)
		if err != nil {
			log.Error("admin: the configured password hash is unusable; signing in with it will fail",
				slog.String("error", err.Error()))
		}
		hash = parsed
	}

	h := &handler{
		deps:     d,
		log:      log,
		password: hash,
		sessions: newSessions(d.Config.SessionTTL.Std()),
		limiter:  newLoginLimiter(),
	}

	r := chi.NewRouter()
	r.Use(h.securityHeaders)
	// Resolved once per request and carried in the context, so the page data,
	// the template set and the script's string table cannot disagree about which
	// language they are producing.
	r.Use(withLang)

	// The language switch. Outside every session check, because the sign-in page
	// and the first-run page both need a switcher and neither has a session yet —
	// which is also why the choice is kept in a cookie rather than on a session.
	r.Get("/lang/{lang}", h.setLanguage)

	// The UI's own assets are served without a session: they contain no data,
	// and a login page that cannot load its stylesheet looks broken in a way
	// that suggests the server is.
	if err := registerAssets(r); err != nil {
		log.Error("admin: the operator UI has no stylesheet", slog.String("error", err.Error()))
	}

	// The built frontend, at every page path under /admin. It is the interface
	// now — there is no second one behind it. See the note in spa.go for why
	// none of this is behind a session check, which the sign-in page in
	// particular could not be.
	registerSPA(r)

	// Signing in and signing up, both outside the session group.
	//
	// Signing in obviously: there is nothing to authenticate against yet. Signing
	// up for the same reason — the account the first-run form creates is the one
	// every later request is checked against, so it cannot itself require one.
	// Both close themselves: sign-in against a hash or a stored credential, and
	// first-run against the row it creates.
	r.Post("/api/login", h.login)
	r.Post("/api/logout", h.logout)
	r.Get("/api/setup", h.setupStatus)
	r.Post("/api/setup", h.createAdministrator)

	// The copy table, and the only one of the client's three read endpoints that
	// is not behind a session.
	//
	// It has to be reachable signed-out, because the sign-in page is client-side
	// and every word on it — the two field labels, the button, the language
	// names — comes from this table. The alternative was a second, smaller table
	// in TypeScript for the four screens that render before a session exists,
	// which is the duplication the i18n package exists to prevent.
	//
	// What that costs is that a stranger can read four hundred sentences naming
	// this service's features. They can also reach the sign-in page, which names
	// the service. Nothing here is a credential, a hostname or an account, and
	// the two endpoints that do describe the deployment — /api/api-docs with its
	// list of every endpoint, and /api/onboarding with its checklist — are still
	// behind the session check below.
	r.Get("/api/i18n", h.i18nJSON)

	// Anything else under /api that no route matched, answered as JSON.
	//
	// Registered before the session group and matched after every route in it,
	// because it is a fallback and not a gate. Without it the client-side
	// interface's own catch-all would serve the shell for /api/typo — an HTML
	// page with a 200 in it, which a caller parsing JSON reports as "the server
	// is broken" rather than as "that endpoint does not exist".
	//
	// Two registrations, and the bare one is not redundant: chi keeps a
	// wildcard's pattern as the prefix that precedes the asterisk, so "/api/*"
	// is a node for "/api/" and a request for "/api" never reaches it. Without
	// the second line the bare path is served the shell — the same failure as
	// above, at the one address a caller is most likely to try by hand.
	r.HandleFunc("/api", h.apiNotFound)
	r.HandleFunc("/api/*", h.apiNotFound)

	r.Group(func(pr chi.Router) {
		pr.Use(h.requireSession)

		pr.Get("/api/session", h.whoami)

		// The first-run checklist's state. The navigation offers the checklist
		// only while something is left to do, so the shell asks for this on
		// every page — see onboardingJSON.
		pr.Get("/api/onboarding", h.onboardingJSON)

		// The API reference, for the page at /admin/api-docs.
		//
		// The content — fourteen endpoints, nine error codes, seven samples —
		// comes from the same functions that generate the sample files, rather
		// than from a second copy written for the page. This endpoint is also
		// why the page is behind a session while the copy table is not: it lists
		// every route this service has.
		pr.Get("/api/api-docs", h.apiDocsJSON)

		pr.Get("/api/channels", h.listChannels)
		pr.Get("/api/channels/types", h.channelTypes)

		// The generated form. `?name=` selects what is being edited; absent
		// means a create.
		//
		// Not /api/channels/form, which is where it reads like it belongs. A
		// channel is named by an operator and `form` is a name somebody could
		// pick; under /api/channels/ the two would collide, and the loser would
		// be the channel — a literal segment beats a parameter in chi, so
		// /api/channels/form would answer with a form and getChannel would be
		// unreachable for that one name. One segment further out, there is
		// nothing to collide with.
		pr.Get("/api/channel-form", h.channelFormJSON)
		pr.Post("/api/channels", h.saveChannel)
		pr.Get("/api/channels/{name}", h.getChannel)
		pr.Delete("/api/channels/{name}", h.deleteChannel)
		pr.Post("/api/channels/{name}/test", h.testChannel)
		pr.Post("/api/channels/{name}/test-notification", h.sendTestNotification)
		pr.Post("/api/channels/{name}/breaker/reset", h.resetBreaker)

		pr.Get("/api/deliveries", h.listDeliveries)
		pr.Get("/api/deliveries/{id}", h.getDelivery)
		pr.Post("/api/deliveries/{id}/replay", h.replayDelivery)
		pr.Get("/api/stats", h.stats)

		pr.Get("/api/audit", h.listAudit)

		pr.Post("/api/password", h.changePassword)

		pr.Get("/api/keys", h.listKeys)
		pr.Post("/api/keys", h.createKey)
		pr.Post("/api/keys/{id}", h.updateKey)
		pr.Delete("/api/keys/{id}", h.deleteKey)
	})

	return r
}

// securityHeaders are the ones that cost nothing and are forgotten most often.
func (h *handler) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The operator UI is same-origin and loads nothing external. Saying so
		// means a mistake that introduces a remote script fails visibly rather
		// than quietly running with the session cookie in reach.
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; "+
				"frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		// A management UI is never meant to be framed, and this is the header
		// that browsers still honour.
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

// requireSession rejects anything without a live session, and anything that
// changes state without the CSRF header.
func (h *handler) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t := copyFor(r)

		cookie, err := r.Cookie(cookieName)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthenticated", t.ErrSignInFirst)
			return
		}

		actor, ok := h.sessions.lookup(cookie.Value)
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthenticated", t.ErrSessionExpired)
			return
		}

		if isStateChanging(r.Method) && r.Header.Get(csrfHeader) == "" {
			writeError(w, http.StatusForbidden, "missing_csrf_header",
				fmt.Sprintf(t.ErrMissingCSRF, csrfHeader))
			return
		}

		next.ServeHTTP(w, r.WithContext(withActor(r.Context(), actor)))
	})
}

func isStateChanging(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

type actorKey struct{}

func withActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, actorKey{}, actor)
}

func actorFrom(ctx context.Context) string {
	actor, _ := ctx.Value(actorKey{}).(string)
	return actor
}

// record writes an operator action to the audit trail.
//
// A failure here is logged and otherwise ignored: refusing the action because
// the audit write failed would make the trail a availability dependency, and
// the action itself has already been decided on. The log line is the fallback,
// and it is the reason this is not silent.
func (h *handler) record(ctx context.Context, action, target, detail string) {
	if h.deps.Audit == nil {
		return
	}
	err := h.deps.Audit.RecordAdminAction(ctx, &store.AdminAction{
		At:     time.Now().UTC(),
		Actor:  actorFrom(ctx),
		Action: action,
		Target: target,
		Detail: detail,
	})
	if err != nil {
		h.log.Error("admin: could not record an operator action",
			slog.String("action", action),
			slog.String("target", target),
			slog.String("error", err.Error()),
		)
	}
}

// ------------------------------------------------------------------ responses

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

type errorBody struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorBody{Error: code, Message: message})
}

// apiNotFound implements the fallback under /api. See the route table.
func (h *handler) apiNotFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotFound, "not_found", copyFor(r).ErrNotFound)
}

// decode reads a JSON body with the limits a request from a browser needs.
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	const maxBody = 1 << 20

	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// cookieSecure reports whether to mark the session cookie Secure.
//
// Decided from the request rather than from configuration: this service is
// commonly deployed behind a TLS-terminating proxy, and a cookie marked Secure
// on a plain-HTTP internal address would simply never be sent, which presents
// as "the login page does not work" rather than as a security setting.
func cookieSecure(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}
