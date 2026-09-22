package admin

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"notifyrelay/internal/admin/i18n"
	"notifyrelay/internal/channel"
	"notifyrelay/internal/config"
	"notifyrelay/internal/router"
	"notifyrelay/internal/store"
)

// The samples are two levels deep because they exist once per language:
// samples/<lang>/<name>.txt. The pattern says so rather than embedding the
// directory, so a sample that ends up at the wrong depth fails the build.
//
//go:embed templates/*.html static/* samples/*/*
var assets embed.FS

// pages holds one parsed template set per language.
//
// Parsed at startup rather than per request: a template error is then a failure
// to start, which is a great deal easier to notice than a 500 on a page nobody
// opened until the day they needed it.
//
// One set per language rather than one set with the language threaded through
// it, because the two template functions that produce copy — the relative time
// and the timestamp — have no way to reach the page's data. A template function
// sees its arguments and nothing else, so a closure over the language is the
// only mechanism that reaches it. Parsing the templates once more at startup
// costs a few milliseconds and removes the problem entirely.
var pages = func() map[i18n.Lang]*template.Template {
	sets := make(map[i18n.Lang]*template.Template, len(i18n.Langs))
	for _, l := range i18n.Langs {
		sets[l] = template.Must(
			template.New("").Funcs(templateFuncs(l)).ParseFS(assets, "templates/*.html"))
	}
	return sets
}()

func templateFuncs(l i18n.Lang) template.FuncMap {
	m := i18n.For(l)

	return template.FuncMap{
		// stamp renders a time the way somebody reading a log wants it: local,
		// to the second, and blank rather than year-zero for an unset field.
		"stamp": m.Stamp,
		"since": m.Since,

		"lower": strings.ToLower,
		"join":  strings.Join,

		// list builds a slice inline, for templates that iterate a fixed set of
		// names. Go templates have no slice literal.
		"list": func(items ...string) []string { return items },

		// quotaValue reads one field of a quota by name, so the form's allowance
		// section is generated from the same five names the store uses rather
		// than from five hand-written inputs that can fall out of step with it.
		"quotaValue": func(q config.QuotaConfig, field string) int {
			switch field {
			case "per_second":
				return q.PerSecond
			case "per_minute":
				return q.PerMinute
			case "per_hour":
				return q.PerHour
			case "per_day":
				return q.PerDay
			case "per_month":
				return q.PerMonth
			default:
				return 0
			}
		},

		// json renders a value into a script block. Used for the two blocks the
		// page carries as data rather than as markup: the channel catalogue and
		// the script's string table.
		"json": func(v any) template.JS {
			out, err := marshalForScript(v)
			if err != nil {
				return template.JS("null")
			}
			return template.JS(out)
		},
	}
}

// ---------------------------------------------------------------- view models

// fieldView is one generated form control.
//
// Everything the browser needs to render and to hide it is here, so the form is
// a function of the schema rather than of a hand-written template. Adding a
// parameter to a channel adds it to the form; the frontend does not change.
type fieldView struct {
	Name        string
	Label       string
	Description string
	Kind        string // text | password | number | checkbox | select | list
	Value       string
	Checked     bool
	Options     []string
	Required    bool
	Private     bool
	// IsSet marks a private parameter that currently holds a value. The input
	// is rendered empty regardless: the browser never receives the credential,
	// and an empty box means "leave it alone" unless Clear is ticked.
	IsSet bool
	Min   string
	Max   string

	// ShowIfField and ShowIfEquals drive the conditional display. Empty means
	// the field is always shown.
	ShowIfField  string
	ShowIfEquals string

	// RequiredNow marks a required field that is always shown, so the template
	// can put `required` on the control itself.
	//
	// A conditional field's control is left to the script, which sets required
	// as the field appears and clears it as the field goes: a hidden input that
	// is still required is a form that cannot be submitted, and the browser
	// reports it against a box nobody can see.
	RequiredNow bool
}

// typeForm is one channel type's generated field set.
//
// Every registered type is rendered into the page and the browser shows the one
// matching the selected type. That is a few kilobytes of HTML and it means the
// form exists in the response rather than being assembled by a script — which
// is what lets a test assert on it.
type typeForm struct {
	Type   string
	Label  string
	Fields []fieldView
}

// buildForm renders a channel type's schema as form fields.
//
// value is the instance's stored configuration, which is empty when the form is
// for a new channel.
func buildForm(d channel.Descriptor, value map[string]any, secretsSet map[string]bool, lang i18n.Lang) typeForm {
	form := typeForm{Type: d.Type, Label: d.Type}
	if d.Type != "" {
		form.Label = strings.ToUpper(d.Type[:1]) + d.Type[1:]
	}

	for _, spec := range d.ParamSchema {
		form.Fields = append(form.Fields,
			buildField(d.Type, spec, value[spec.Name], secretsSet[spec.Name], lang))
	}
	return form
}

func buildField(channelType string, spec channel.ParamSpec, raw any, isSet bool, lang i18n.Lang) fieldView {
	f := fieldView{
		Name:        spec.Name,
		Label:       spec.Label,
		Description: spec.Desc,
		Private:     spec.Private,
		IsSet:       isSet,
		Required:    requiredWhenShown(spec),
		Kind:        inputKind(spec),
		Options:     spec.Values,
	}

	if spec.Label == "" {
		f.Label = spec.Name
	}

	// The schema's English is the declaration; a translation overlays it. What
	// is not translated stays English rather than going blank, so a channel type
	// nobody has translated yet still produces a usable form — and a test
	// asserts that never happens for the types this build ships.
	if c, ok := i18n.LookupParam(lang, channelType, spec.Name); ok {
		if c.Label != "" {
			f.Label = c.Label
		}
		if c.Desc != "" {
			f.Description = c.Desc
		}
	}
	if spec.Min != nil {
		f.Min = trimNumber(*spec.Min)
	}
	if spec.Max != nil {
		f.Max = trimNumber(*spec.Max)
	}
	if spec.ShowIf != nil {
		f.ShowIfField = spec.ShowIf.Field
		f.ShowIfEquals = fmt.Sprint(spec.ShowIf.Equals)
	}
	f.RequiredNow = f.Required && f.ShowIfField == ""

	// A private value is never rendered, whatever was passed in. The caller is
	// supposed to have masked it already; doing it again here means a future
	// caller that forgets cannot turn the form into a way of reading
	// credentials out of the database.
	if spec.Private {
		return f
	}

	switch spec.Type {
	case channel.ParamBool:
		b, _ := raw.(bool)
		f.Checked = b
	case channel.ParamStringList:
		f.Value = joinList(raw)
	default:
		if raw != nil {
			f.Value = fmt.Sprint(raw)
		} else if spec.Default != nil {
			f.Value = fmt.Sprint(spec.Default)
		}
	}
	return f
}

// requiredWhenShown applies the rule ParamSpec.ShowIf documents: a conditional
// parameter is required when it applies, unless it declares a default.
//
// A boolean is never required — an unchecked box is a value — and neither is a
// field the operator can leave to the channel's own default.
func requiredWhenShown(spec channel.ParamSpec) bool {
	if spec.Required {
		return true
	}
	if spec.ShowIf == nil {
		return false
	}
	if spec.Type == channel.ParamBool {
		return false
	}
	return spec.Default == nil
}

func inputKind(spec channel.ParamSpec) string {
	if spec.Private {
		return "password"
	}
	switch spec.Type {
	case channel.ParamBool:
		return "checkbox"
	case channel.ParamEnum:
		return "select"
	case channel.ParamInt, channel.ParamFloat:
		return "number"
	case channel.ParamStringList:
		return "list"
	default:
		return "text"
	}
}

func trimNumber(v float64) string {
	if v == float64(int64(v)) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

func joinList(raw any) string {
	switch v := raw.(type) {
	case []string:
		return strings.Join(v, ", ")
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			out = append(out, fmt.Sprint(item))
		}
		return strings.Join(out, ", ")
	case string:
		return v
	default:
		return ""
	}
}

// ------------------------------------------------------------- page handlers

type pageData struct {
	Title string
	Actor string
	Nav   string

	// Lang is the language this page is being rendered in, T is that language's
	// copy table, and HTMLTag is the value for the html element's lang
	// attribute.
	//
	// Every template reads its words from T. The table is a struct, so a
	// template naming a field that does not exist is a parse error at startup
	// rather than a blank space on a page.
	Lang    i18n.Lang
	T       *i18n.Messages
	HTMLTag string

	// LangSwitch is the list of languages this build has, each with a link that
	// selects it and returns to this page.
	LangSwitch []langOption

	// Steps is the first-run checklist's state.
	//
	// Computed for every page because the navigation offers the checklist only
	// while something is left to do, and a link that is always there is a link
	// nobody clicks twice. It is empty on the pages rendered without a session,
	// where there is nothing to check.
	Steps Onboarding

	// Flash is a message from the previous action, carried in the query string
	// rather than in a session: a redirect after a form post should say what
	// happened, and a one-shot query parameter is the smallest thing that does.
	//
	// The script drops the parameter from the address bar once the page has it.
	// Reading it is a one-shot event; leaving it in the URL means a reload
	// replays a message about something that happened ten minutes ago, and a
	// pasted link carries somebody else's.
	Flash string
	Error string

	// Back and BackLabel are the error page's way out. A page that failed while
	// reading deliveries should offer deliveries: the operator was in the middle
	// of something, and the only useful link is the one back to it.
	Back      string
	BackLabel string
}

// navHome is where each page's error sends the reader back to.
//
// The error page used to carry one fixed link to the channel list, which is the
// wrong page for everything that is not the channel list — and it was worst
// exactly where it mattered most, since a failed delivery lookup offered a
// detour through channel configuration.
var navHome = map[string]string{
	"channels":   "/admin/channels",
	"deliveries": "/admin/deliveries",
	"audit":      "/admin/audit",
	"keys":       "/admin/keys",
	"api-docs":   "/admin/api-docs",
	"setup":      "/admin/setup",
}

// backFor resolves a nav key to the error page's link and its label, defaulting
// to the channel list so an unrecognised page still offers somewhere to go.
func backFor(t *i18n.Messages, nav string) (string, string) {
	switch nav {
	case "deliveries":
		return navHome[nav], t.BackToDeliveries
	case "audit":
		return navHome[nav], t.BackToAudit
	case "keys":
		return navHome[nav], t.BackToKeys
	case "api-docs":
		return navHome[nav], t.BackToAPI
	case "setup":
		return navHome[nav], t.BackToSetup
	}
	return navHome["channels"], t.BackToChannels
}

func (h *handler) render(w http.ResponseWriter, r *http.Request, page string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	set := pages[langFrom(r.Context())]
	if set == nil {
		set = pages[i18n.Default]
	}

	if err := set.ExecuteTemplate(w, page, data); err != nil {
		// The header is already sent, so the status cannot be changed. Log it:
		// a template that fails at render time is a bug that would otherwise
		// show up as a half-written page.
		h.log.Error("admin: rendering a page failed",
			slog.String("page", page), slog.String("error", err.Error()))
	}
}

// pageHandler wraps a page in the session check.
//
// The API answers 401 with JSON, which a browser would render as a blank page.
// A page has to send the operator somewhere they can sign in, so the session is
// checked here and a redirect is what a missing one produces.
func (h *handler) pageHandler(next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Checked before the session, because a deployment with no
		// administrator has nothing to sign in to: sending the first visitor to
		// a sign-in form for an account that does not exist is a dead end that
		// looks like a bug.
		//
		// An error here falls through to the ordinary session check rather than
		// redirecting. A store that cannot answer must not be read as "unclaimed
		// deployment" — that would hand the setup page to whoever asked next.
		if required, err := h.setupRequired(r.Context()); err == nil && required {
			http.Redirect(w, r, "/admin/setup", http.StatusFound)
			return
		}

		cookie, err := r.Cookie(cookieName)
		if err != nil {
			http.Redirect(w, r, "/admin/login", http.StatusFound)
			return
		}
		actor, ok := h.sessions.lookup(cookie.Value)
		if !ok {
			http.Redirect(w, r, "/admin/login", http.StatusFound)
			return
		}
		next(w, r, actor)
	}
}

// loginPage implements GET /admin/login.
func (h *handler) loginPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "login.html", h.pageBase(r, "", "login"))
}

// channelsPage implements GET /admin/channels.
func (h *handler) channelsPage(w http.ResponseWriter, r *http.Request, actor string) {
	ctx := r.Context()
	lang := langFrom(ctx)

	stored, err := h.deps.Channels.Load(ctx)
	if err != nil {
		h.renderError(w, r, actor, "channels", copyFor(r).ErrChannelsUnreadable, err)
		return
	}

	// Every registered type is rendered, so switching type in the form is a
	// display change rather than a round trip.
	descriptors := channel.Descriptors()
	forms := make([]typeForm, 0, len(descriptors))
	for _, d := range descriptors {
		forms = append(forms, buildForm(d, nil, nil, lang))
	}
	forms = sortedTypes(forms)

	editing := r.URL.Query().Get("edit")
	var (
		editType    string
		editEnabled = true
		editQuota   config.QuotaConfig
		editSecrets = map[string]bool{}
		// editMissing marks a link to a channel that is not there any more.
		//
		// The form used to be rendered anyway: the lookup returned nil, the
		// per-type block kept its blank values, and the page showed an empty
		// form headed "Edit X" whose save would create X. An operator following
		// a stale link got a form that looked like an edit and was a create,
		// which is the kind of thing you find out about afterwards.
		editMissing bool
	)
	if editing != "" && editing != newChannel {
		cfg, err := h.deps.Channels.Get(ctx, editing)
		if err != nil {
			h.renderError(w, r, actor, "channels", copyFor(r).ErrChannelUnreadable, err)
			return
		}
		if cfg == nil {
			editMissing = true
		} else {
			_, set := config.MaskSecrets(cfg.Type, cfg.Config)
			for _, name := range set {
				editSecrets[name] = true
			}
			if d, ok := channel.Lookup(cfg.Type); ok {
				f := buildForm(d, cfg.Config, editSecrets, lang)

				// The form is rendered from the per-type list, so the type
				// being edited has to be replaced in it. Rendering the blank
				// catalogue entry instead is how an operator opens a channel,
				// sees empty boxes, saves, and loses the host and the
				// recipients — the private parameters survive a merge, and
				// nothing else does.
				for i := range forms {
					if forms[i].Type == f.Type {
						forms[i] = f
						break
					}
				}
			}
			editType = cfg.Type
			editEnabled = cfg.IsEnabled()
			editQuota = cfg.Quota
		}
	}

	views := make([]channelView, 0, len(stored))
	for _, cfg := range stored {
		views = append(views, h.view(cfg))
	}

	h.render(w, r, "channels.html", struct {
		pageData
		Channels    []channelView
		Forms       []typeForm
		Editing     string
		EditMissing bool
		EditType    string
		EditEnabled bool
		EditQuota   config.QuotaConfig
		EditSecrets map[string]bool
		Catalog     template.JS
	}{
		pageData:    h.pageBase(r, actor, "channels"),
		Channels:    views,
		Forms:       forms,
		Editing:     editing,
		EditMissing: editMissing,
		EditType:    editType,
		EditEnabled: editEnabled,
		EditQuota:   editQuota,
		EditSecrets: editSecrets,
		Catalog:     catalogJSON(h.deps.Router),
	})
}

// deliveriesPage implements GET /admin/deliveries.
func (h *handler) deliveriesPage(w http.ResponseWriter, r *http.Request, actor string) {
	query := r.URL.Query()

	filter := store.Filter{
		Status:    store.Status(query.Get("status")),
		Target:    query.Get("target"),
		RequestID: query.Get("request_id"),
	}
	if raw := query.Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 500 {
			filter.Limit = n
		}
	}
	if filter.Limit == 0 {
		filter.Limit = 50
	}
	if raw := query.Get("offset"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			filter.Offset = n
		}
	}

	var (
		views []deliveryView
		stats store.Stats
	)
	if h.deps.Deliveries != nil {
		deliveries, err := h.deps.Deliveries.List(r.Context(), filter)
		if err != nil {
			h.renderError(w, r, actor, "deliveries", copyFor(r).ErrDeliveriesUnreadable, err)
			return
		}
		for _, d := range deliveries {
			views = append(views, h.viewDelivery(d))
		}
		if s, err := h.deps.Deliveries.Stats(r.Context()); err == nil {
			stats = s
		}
	}

	h.render(w, r, "deliveries.html", struct {
		pageData
		Deliveries []deliveryView
		Stats      store.Stats
		Status     string
		Target     string
		RequestID  string
		Limit      int
		Offset     int
		Next       int
		Prev       int
	}{
		pageData:   h.pageBase(r, actor, "deliveries"),
		Deliveries: views,
		Stats:      stats,
		Status:     string(filter.Status),
		Target:     filter.Target,
		RequestID:  filter.RequestID,
		Limit:      filter.Limit,
		Offset:     filter.Offset,
		Next:       filter.Offset + filter.Limit,
		Prev:       max(filter.Offset-filter.Limit, 0),
	})
}

// deliveryPage implements GET /admin/deliveries/{id}.
func (h *handler) deliveryPage(w http.ResponseWriter, r *http.Request, actor string) {
	id := chi.URLParam(r, "id")

	if h.deps.Deliveries == nil {
		h.renderError(w, r, actor, "deliveries", copyFor(r).ErrNoDeliveryStore, nil)
		return
	}

	d, err := h.deps.Deliveries.Get(r.Context(), id)
	if err != nil {
		h.renderError(w, r, actor, "deliveries", copyFor(r).ErrDeliveryUnreadable, err)
		return
	}
	if d == nil {
		h.renderError(w, r, actor, "deliveries", copyFor(r).ErrDeliveryNotFound, nil)
		return
	}

	attempts, err := h.deps.Deliveries.Attempts(r.Context(), id)
	if err != nil {
		h.renderError(w, r, actor, "deliveries", copyFor(r).ErrAttemptsUnreadable, err)
		return
	}

	views := make([]attemptView, 0, len(attempts))
	for _, a := range attempts {
		views = append(views, attemptView{
			AttemptNo: a.AttemptNo, Class: a.Class, SkipReason: a.SkipReason,
			Detail: a.Detail, Error: a.Error, ElapsedMS: a.ElapsedMS,
			CreatedAt: a.CreatedAt, ChannelType: a.ChannelType, Target: a.Target,
		})
	}

	// The nav key stays "deliveries" so the sidebar keeps the section lit, and
	// the title is set separately: a tab reading "Deliveries" over a page about
	// one delivery is the tab promising a list that is not there.
	base := h.pageBase(r, actor, "deliveries")
	base.Title = base.T.TitleDelivery

	h.render(w, r, "delivery.html", struct {
		pageData
		Delivery deliveryView
		Attempts []attemptView
	}{
		pageData: base,
		Delivery: h.viewDelivery(d),
		Attempts: views,
	})
}

// auditPage implements GET /admin/audit.
func (h *handler) auditPage(w http.ResponseWriter, r *http.Request, actor string) {
	var views []auditView

	if h.deps.Audit != nil {
		actions, err := h.deps.Audit.ListAdminActions(r.Context(), 200)
		if err != nil {
			h.renderError(w, r, actor, "audit", copyFor(r).ErrAuditUnreadable, err)
			return
		}
		for _, a := range actions {
			views = append(views, auditView{At: a.At, Actor: a.Actor, Action: a.Action, Target: a.Target, Detail: a.Detail})
		}
	}

	h.render(w, r, "audit.html", struct {
		pageData
		Actions []auditView
		// Enabled distinguishes "this deployment keeps no audit trail" from
		// "it does, and nothing has happened yet". Both render as an empty
		// table, and telling them apart is the difference between a fact about
		// the deployment and a fact about the operator's afternoon.
		Enabled bool
	}{
		pageData: h.pageBase(r, actor, "audit"),
		Actions:  views,
		Enabled:  h.deps.Audit != nil,
	})
}

// -------------------------------------------------------------------- helpers

func (h *handler) pageBase(r *http.Request, actor, nav string) pageData {
	lang := langFrom(r.Context())
	t := copyFor(r)

	data := pageData{
		Title:      titleFor(t, nav),
		Actor:      actor,
		Nav:        nav,
		Lang:       lang,
		T:          t,
		HTMLTag:    lang.HTMLTag(),
		LangSwitch: langOptions(r, lang),
		Flash:      r.URL.Query().Get("ok"),
		Error:      r.URL.Query().Get("err"),
	}

	// Only where there is a session. The sign-in and first-run pages have
	// nothing to check, and asking the stores on the way to a page that shows
	// no navigation at all would be three reads for a link that is not drawn.
	if actor != "" {
		data.Steps = h.onboarding(r)
	}

	return data
}

// titleFor is a page's title from the copy table.
//
// It used to be built from the nav key by capitalising it, which produced
// "Api-docs" for the reference page and would have produced nothing usable at
// all once the interface was not in English.
func titleFor(t *i18n.Messages, nav string) string {
	switch nav {
	case "channels":
		return t.TitleChannels
	case "deliveries":
		return t.TitleDeliveries
	case "audit":
		return t.TitleAudit
	case "keys":
		return t.TitleKeys
	case "api-docs":
		return t.TitleAPI
	case "start":
		return t.TitleStart
	case "password":
		return t.TitlePassword
	case "login":
		return t.TitleSignIn
	case "setup":
		return t.TitleSetup
	case "delivery":
		return t.TitleDelivery
	}
	return t.AppName
}

// renderError shows the error as a page rather than a JSON body.
//
// The underlying error is logged and not shown: an operator gets a sentence
// they can act on, and the detail that might name a file path or a query stays
// in the log where it belongs.
//
// message is display copy and nothing else. It used to be the log line too,
// which was fine while there was one language: now it is the language the
// request asked for, and a log whose lines change with the caller's cookie is a
// log nobody can grep. The line carries the page's stable key instead.
func (h *handler) renderError(w http.ResponseWriter, r *http.Request, actor, nav, message string, err error) {
	if err != nil {
		h.log.Error("admin: could not render a page",
			slog.String("page", nav), slog.String("error", err.Error()))
	}

	data := h.pageBase(r, actor, nav)
	// The title becomes the error's, not the page's. A tab reading "Deliveries"
	// next to a page saying the deliveries could not be read is a tab that
	// promises something the page does not have.
	data.Title = data.T.TitleError
	data.Error = message
	data.Back, data.BackLabel = backFor(data.T, nav)
	w.WriteHeader(http.StatusInternalServerError)
	h.render(w, r, "error.html", data)
}

// registerAssets serves the embedded CSS and JavaScript.
//
// One ordinary route per file rather than a "/static/*" subtree. A subtree
// route inside a mounted router does not survive the mount: the router matches
// it when called directly and 404s when called through Mount, because the mount
// rewrites the route path the subtree pattern is matched against. Two files do
// not need a subtree, and discovering them keeps adding a third from requiring
// a code change.
func registerAssets(r chi.Router) error {
	entries, err := fs.ReadDir(assets, "static")
	if err != nil {
		return fmt.Errorf("admin: reading the embedded assets: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		file, err := fs.ReadFile(assets, "static/"+name)
		if err != nil {
			return fmt.Errorf("admin: reading static/%s: %w", name, err)
		}

		body := file
		contentType := mime.TypeByExtension(path.Ext(name))
		if contentType == "" {
			contentType = "application/octet-stream"
		}

		r.Get("/"+name, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", contentType)
			_, _ = w.Write(body)
		})
	}
	return nil
}

// catalogJSON is the schema catalogue as the page embeds it.
//
// The form itself is rendered server-side; this exists so the browser can
// re-render a field set when the operator switches channel type without a round
// trip, and so anything else that needs the schema reads the same declaration
// the server used.
func catalogJSON(r *router.Router) template.JS {
	entries := router.Catalog(r)

	// encoding/json would be the obvious tool, but the result goes into a
	// script tag and a channel description containing "</script>" would end it.
	// The descriptions are written by us, and escaping them anyway is cheaper
	// than remembering that they are trusted.
	out, err := marshalForScript(entries)
	if err != nil {
		return template.JS("[]")
	}
	return template.JS(out)
}

func marshalForScript(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	s := string(raw)
	s = strings.ReplaceAll(s, "<", `<`)
	s = strings.ReplaceAll(s, ">", `>`)
	s = strings.ReplaceAll(s, "&", `&`)
	return s, nil
}

// sortedTypes is a stable order for the type picker, so the list does not
// reshuffle between page loads. channel.Descriptors already sorts by type; this
// keeps that true if it ever stops.
func sortedTypes(forms []typeForm) []typeForm {
	sort.Slice(forms, func(i, j int) bool { return forms[i].Type < forms[j].Type })
	return forms
}
