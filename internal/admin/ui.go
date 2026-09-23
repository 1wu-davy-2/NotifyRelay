package admin

import (
	"embed"
	"fmt"
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
)

// The samples are two levels deep because they exist once per language:
// samples/<lang>/<name>.txt. The pattern says so rather than embedding the
// directory, so a sample that ends up at the wrong depth fails the build.
//
// static/ is one file now. The stylesheet it holds is what the unbuilt handler
// borrows — embedded rather than written into that page, because the CSP
// forbids an inline <style> and the page would then render unstyled in exactly
// the situation it exists for. See unbuiltHandler in spa.go.
//
//go:embed static/* samples/*/*
var assets embed.FS

// ---------------------------------------------------------------- view models

// fieldView is one generated form control.
//
// Everything the browser needs to render and to hide it is here, so the form is
// a function of the schema rather than of a hand-written template. Adding a
// parameter to a channel adds it to the form; the frontend does not change.
// Tagged for JSON as well as read by the template. The client-side interface
// renders the same form from this same struct rather than re-deriving it from
// the schema — see channelFormJSON for why that matters.
type fieldView struct {
	Name        string   `json:"name"`
	Label       string   `json:"label"`
	Description string   `json:"description,omitempty"`
	Kind        string   `json:"kind"` // text | password | number | checkbox | select | list
	Value       string   `json:"value"`
	Checked     bool     `json:"checked"`
	Options     []string `json:"options,omitempty"`
	Required    bool     `json:"required"`
	Private     bool     `json:"private"`
	// IsSet marks a private parameter that currently holds a value. The input
	// is rendered empty regardless: the browser never receives the credential,
	// and an empty box means "leave it alone" unless Clear is ticked.
	IsSet bool   `json:"is_set"`
	Min   string `json:"min,omitempty"`
	Max   string `json:"max,omitempty"`

	// ShowIfField and ShowIfEquals drive the conditional display. Empty means
	// the field is always shown.
	ShowIfField  string `json:"show_if_field,omitempty"`
	ShowIfEquals string `json:"show_if_equals,omitempty"`

	// RequiredNow marks a required field that is always shown, so the template
	// can put `required` on the control itself.
	//
	// A conditional field's control is left to the script, which sets required
	// as the field appears and clears it as the field goes: a hidden input that
	// is still required is a form that cannot be submitted, and the browser
	// reports it against a box nobody can see.
	RequiredNow bool `json:"required_now"`
}

// typeForm is one channel type's generated field set.
//
// Every registered type is rendered into the page and the browser shows the one
// matching the selected type. That is a few kilobytes of HTML and it means the
// form exists in the response rather than being assembled by a script — which
// is what lets a test assert on it.
type typeForm struct {
	Type   string      `json:"type"`
	Label  string      `json:"label"`
	Fields []fieldView `json:"fields"`
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

// ------------------------------------------------------------ the channel form

// channelFormData is everything the channel form needs.
//
// Built once and consumed twice — by channels.html through html/template, and
// by the client-side form through channelFormJSON. One builder rather than two,
// because the alternative is a second implementation of "which fields does this
// channel type have, what are they called, which are required, which are
// hidden behind a condition, and what does the stored value look like".
//
// That second implementation is not hypothetical and it is not cheap to get
// right. It has to reproduce requiredWhenShown — which a schema cannot express,
// because a conditional parameter with a default is required when it applies
// and one without a default is not. It has to reproduce the default's rendering,
// where a numeric default of zero is a value the form shows and the JSON omits,
// so a client deriving "is there a default" from the wire gets the wrong answer
// for exactly the parameters that matter. Both bugs are silent and both produce
// a form that asks for something the server does not want.
type channelFormData struct {
	Forms []typeForm `json:"forms"`
	// Editing is the name being edited, or newChannel for a create.
	Editing string `json:"editing"`
	// Missing marks a link to a channel that is not there any more. The form
	// used to be rendered anyway: the lookup returned nil, the per-type block
	// kept its blank values, and the page showed an empty form headed "Edit X"
	// whose save would create X. An operator following a stale link got a form
	// that looked like an edit and was a create, which is the kind of thing you
	// find out about afterwards.
	Missing     bool               `json:"missing"`
	EditType    string             `json:"edit_type,omitempty"`
	EditEnabled bool               `json:"edit_enabled"`
	EditQuota   config.QuotaConfig `json:"edit_quota"`
	// EditSecrets names the private parameters that already hold a value. Not
	// the values: the browser is told that a credential exists and nothing more.
	EditSecrets map[string]bool `json:"edit_secrets,omitempty"`
}

// channelForm builds the form for a create or an edit.
//
// editing is a channel name, or newChannel, or empty for a create.
func (h *handler) channelForm(r *http.Request, editing string) (channelFormData, error) {
	ctx := r.Context()
	lang := langFrom(ctx)

	// Every registered type is rendered, so switching type in the form is a
	// display change rather than a round trip.
	descriptors := channel.Descriptors()
	forms := make([]typeForm, 0, len(descriptors))
	for _, d := range descriptors {
		forms = append(forms, buildForm(d, nil, nil, lang))
	}
	forms = sortedTypes(forms)

	out := channelFormData{
		Forms:       forms,
		Editing:     editing,
		EditEnabled: true,
		EditSecrets: map[string]bool{},
	}

	if editing == "" || editing == newChannel {
		return out, nil
	}

	cfg, err := h.deps.Channels.Get(ctx, editing)
	if err != nil {
		return out, err
	}
	if cfg == nil {
		out.Missing = true
		return out, nil
	}

	_, set := config.MaskSecrets(cfg.Type, cfg.Config)
	for _, name := range set {
		out.EditSecrets[name] = true
	}
	if d, ok := channel.Lookup(cfg.Type); ok {
		f := buildForm(d, cfg.Config, out.EditSecrets, lang)

		// The form is rendered from the per-type list, so the type being edited
		// has to be replaced in it. Rendering the blank catalogue entry instead
		// is how an operator opens a channel, sees empty boxes, saves, and
		// loses the host and the recipients — the private parameters survive a
		// merge, and nothing else does.
		for i := range out.Forms {
			if out.Forms[i].Type == f.Type {
				out.Forms[i] = f
				break
			}
		}
	}

	out.EditType = cfg.Type
	out.EditEnabled = cfg.IsEnabled()
	out.EditQuota = cfg.Quota
	return out, nil
}

// channelFormJSON implements GET /admin/api/channels/form.
//
// The client-side form renders from this rather than from the raw parameter
// schema at /api/channels/types. See channelFormData for why: the schema is the
// input to the form's construction, not the form, and the difference between
// the two is a set of rules the server already implements and a client would
// have to implement again.
//
// `?name=` selects what is being edited and is absent for a create.
func (h *handler) channelFormJSON(w http.ResponseWriter, r *http.Request) {
	data, err := h.channelForm(r, r.URL.Query().Get("name"))
	if err != nil {
		h.log.Error("admin: building the channel form failed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal", copyFor(r).ErrChannelUnreadable)
		return
	}
	writeJSON(w, http.StatusOK, data)
}

// -------------------------------------------------------------------- helpers

// registerAssets serves the embedded stylesheet and favicon.
//
// One ordinary route per file rather than a "/static/*" subtree. A subtree
// route inside a mounted router does not survive the mount: the router matches
// it when called directly and 404s when called through Mount, because the mount
// rewrites the route path the subtree pattern is matched against. Two files do
// not need a subtree, and discovering them keeps adding a third from requiring
// a code change.
//
// The stylesheet is the one file left over from the server-rendered interface.
// It is here for the unbuilt handler, which borrows it because the CSP forbids
// the inline <style> that page would otherwise need; the interface itself
// carries its own styles, bundled into the content-hashed asset tree.
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

// sortedTypes is a stable order for the type picker, so the list does not
// reshuffle between page loads. channel.Descriptors already sorts by type; this
// keeps that true if it ever stops.
func sortedTypes(forms []typeForm) []typeForm {
	sort.Slice(forms, func(i, j int) bool { return forms[i].Type < forms[j].Type })
	return forms
}
