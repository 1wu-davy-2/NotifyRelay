package admin

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"notifyrelay/internal/channel"
	"notifyrelay/internal/config"
	"notifyrelay/internal/router"
)

// channelView is a channel instance as the UI sees it.
//
// Config carries no secret values. Which secrets are set is reported separately
// by SecretsSet, because a form has to be able to say "a password is configured"
// without the browser ever holding the password.
type channelView struct {
	Name    string             `json:"name"`
	Type    string             `json:"type"`
	Enabled bool               `json:"enabled"`
	Config  map[string]any     `json:"config"`
	Quota   config.QuotaConfig `json:"quota"`
	// SecretsSet names the private parameters that currently have a value.
	SecretsSet []string `json:"secrets_set,omitempty"`
	// Live reports whether this instance is currently loaded in the router —
	// which is not the same as existing, and the difference is what a failed
	// reload looks like from the outside.
	Live      bool      `json:"live"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

func (h *handler) view(cfg config.ChannelConfig) channelView {
	masked, set := config.MaskSecrets(cfg.Type, cfg.Config)

	live := false
	if h.deps.Router != nil {
		live = h.deps.Router.TypeOf(cfg.Name) != ""
	}

	return channelView{
		Name:       cfg.Name,
		Type:       cfg.Type,
		Enabled:    cfg.IsEnabled(),
		Config:     masked,
		Quota:      cfg.Quota,
		SecretsSet: set,
		Live:       live,
	}
}

type channelsResponse struct {
	Channels []channelView `json:"channels"`
}

// listChannels implements GET /admin/api/channels.
func (h *handler) listChannels(w http.ResponseWriter, r *http.Request) {
	stored, err := h.deps.Channels.Load(r.Context())
	if err != nil {
		h.log.Error("admin: listing channels failed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal", "the channels could not be read")
		return
	}

	out := make([]channelView, 0, len(stored))
	for _, cfg := range stored {
		out = append(out, h.view(cfg))
	}
	writeJSON(w, http.StatusOK, channelsResponse{Channels: out})
}

// getChannel implements GET /admin/api/channels/{name}.
func (h *handler) getChannel(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.deps.Channels.Get(r.Context(), chi.URLParam(r, "name"))
	if err != nil {
		h.log.Error("admin: reading a channel failed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal", "the channel could not be read")
		return
	}
	if cfg == nil {
		writeError(w, http.StatusNotFound, "not_found", "no channel with that name")
		return
	}
	writeJSON(w, http.StatusOK, h.view(*cfg))
}

// channelTypes implements GET /admin/api/channels/types.
//
// The same catalogue /api/v1/channels serves. The UI generates its form from
// this, which is what keeps a new channel type from requiring a frontend
// change.
func (h *handler) channelTypes(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"channels": router.Catalog(h.deps.Router)})
}

// newChannel is the sentinel the "New channel" link uses as its edit target.
//
// It is not a channel name and must not be looked up as one: the lookup returns
// nil, the form is skipped, and the button appears to do nothing at all. It is
// also what the form sends back as its Editing value, which is how the server
// tells a create from an edit.
const newChannel = "__new__"

// saveRequest is the body of POST /admin/api/channels.
type saveRequest struct {
	Name    string             `json:"name"`
	Type    string             `json:"type"`
	Enabled *bool              `json:"enabled"`
	Config  map[string]any     `json:"config"`
	Quota   config.QuotaConfig `json:"quota"`

	// Editing is what the caller believed it was doing: newChannel when the
	// form was opened to create, the channel's own name when it was opened to
	// change, and empty when the caller has no such notion — a script posting a
	// desired state, which is what this endpoint has always accepted.
	Editing string `json:"editing,omitempty"`

	// Replace says the caller has seen the name collision and means it anyway.
	Replace bool `json:"replace,omitempty"`
}

// saveChannel implements POST /admin/api/channels.
//
// One endpoint for create and update: the caller's intent is "this is what the
// channel should be", and a separate create would have to answer what happens
// when the name already exists. It answers that question here instead, and only
// when the caller has said it meant to create.
func (h *handler) saveChannel(w http.ResponseWriter, r *http.Request) {
	var req saveRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "the request body is not valid JSON")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "a channel name is required")
		return
	}

	cfg := config.ChannelConfig{
		Name:    req.Name,
		Type:    req.Type,
		Enabled: req.Enabled,
		Config:  req.Config,
		Quota:   req.Quota,
	}
	if cfg.Config == nil {
		cfg.Config = map[string]any{}
	}

	existing, err := h.deps.Channels.Get(r.Context(), req.Name)
	if err != nil {
		h.log.Error("admin: reading a channel failed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal", "the channel could not be read")
		return
	}

	// A new-channel form that names an existing channel is a collision, not an
	// edit. Saving it would replace a channel that is delivering right now, and
	// the operator asked to create one — so the answer is a question rather
	// than a write, and the question names what it would have replaced.
	//
	// Only when the caller said it meant to create. An empty Editing is a
	// client posting a desired state, which is what this endpoint has always
	// been for, and refusing that would break every script using it.
	if existing != nil && req.Editing == newChannel && !req.Replace {
		writeError(w, http.StatusConflict, "name_taken",
			"a channel named "+req.Name+" already exists; saving would replace it")
		return
	}

	// Merge first, then validate, then store. The order matters and is not
	// obvious: a form cannot send back a credential it was never shown, so the
	// configuration that will actually be stored is the merged one — and
	// validating what the client sent would refuse an edit that changes a
	// channel's host, complaining about the password the form was never given.
	cfg, err = h.deps.Channels.MergeEdit(r.Context(), cfg)
	if err != nil {
		h.log.Error("admin: merging a channel edit failed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal", "the channel could not be read")
		return
	}

	// Validate before storing, using the channel's own constructor and schema.
	// Storing first and discovering the problem at reload time would leave the
	// database holding a configuration the service cannot run.
	if err := validateChannel(cfg); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_config", err.Error())
		return
	}

	if err := h.deps.Channels.Save(r.Context(), cfg); err != nil {
		h.log.Error("admin: saving a channel failed", slog.String("error", err.Error()))
		writeError(w, http.StatusBadRequest, "save_failed", err.Error())
		return
	}

	action, detail := "channel.create", "created"
	if existing != nil {
		action, detail = "channel.update", describeChange(*existing, cfg)
	}
	h.record(r.Context(), action, cfg.Name, detail)

	if err := h.reload(r.Context()); err != nil {
		// The configuration is stored and valid; only the running router
		// refused it. Reporting the failure is the point — the operator needs
		// to know the change is not live, and the channel view says so too.
		h.log.Error("admin: reload after saving a channel failed", slog.String("error", err.Error()))
		writeJSON(w, http.StatusAccepted, map[string]any{
			"saved":  true,
			"live":   false,
			"reload": err.Error(),
		})
		return
	}

	saved, err := h.deps.Channels.Get(r.Context(), req.Name)
	if err != nil || saved == nil {
		writeJSON(w, http.StatusOK, map[string]any{"saved": true, "live": true})
		return
	}
	writeJSON(w, http.StatusOK, h.view(*saved))
}

// deleteChannel implements DELETE /admin/api/channels/{name}.
func (h *handler) deleteChannel(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	existed, err := h.deps.Channels.Delete(r.Context(), name)
	if err != nil {
		h.log.Error("admin: deleting a channel failed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal", "the channel could not be deleted")
		return
	}
	if !existed {
		writeError(w, http.StatusNotFound, "not_found", "no channel with that name")
		return
	}

	h.record(r.Context(), "channel.delete", name, "deleted")

	// A failed reload after a delete is worth reporting but not worth failing
	// on: the row is gone, which is what was asked for, and the router still
	// holds the old instance until it is reloaded.
	if err := h.reload(r.Context()); err != nil {
		h.log.Error("admin: reload after deleting a channel failed", slog.String("error", err.Error()))
		writeJSON(w, http.StatusAccepted, map[string]any{"deleted": true, "live": false, "reload": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true, "live": true})
}

// testChannel implements POST /admin/api/channels/{name}/test.
//
// It builds a fresh instance from the stored configuration rather than reaching
// into the router, so the test reports what the *stored* configuration does —
// including for a channel that failed to load and is therefore not live.
func (h *handler) testChannel(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	cfg, err := h.deps.Channels.Get(r.Context(), name)
	if err != nil || cfg == nil {
		writeError(w, http.StatusNotFound, "not_found", "no channel with that name")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	res := h.probe(ctx, *cfg)

	// Test results are not audited: a connectivity check changes nothing, and
	// a trail full of them would bury the actions that did.
	writeJSON(w, http.StatusOK, map[string]any{
		"channel": name,
		"class":   res.Class.String(),
		"ok":      res.Class == channel.ClassSent,
		"detail":  res.Detail,
		"error":   errorText(res),
	})
}

// probe builds and tests a channel instance.
func (h *handler) probe(ctx context.Context, cfg config.ChannelConfig) channel.Result {
	ch, err := channel.New(cfg.Type, cfg.Name, cfg.Config)
	if err != nil {
		return channel.Permanent(err, "the channel could not be built from its configuration")
	}
	return ch.Test(ctx)
}

// errorText renders a result's error without ever returning nil as a string.
func errorText(res channel.Result) string {
	if res.Err == nil {
		return ""
	}
	return res.Err.Error()
}

// resetBreaker implements POST /admin/api/channels/{name}/breaker/reset.
func (h *handler) resetBreaker(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	if h.deps.Breakers == nil {
		writeError(w, http.StatusNotImplemented, "breaker_disabled",
			"the circuit breaker is not enabled in this deployment")
		return
	}

	// The channel must exist. Resetting the breaker of a name nobody has
	// configured would create breaker state for a channel that is not there.
	cfg, err := h.deps.Channels.Get(r.Context(), name)
	if err != nil || cfg == nil {
		writeError(w, http.StatusNotFound, "not_found", "no channel with that name")
		return
	}

	was := h.deps.Breakers.Reset(r.Context(), name, time.Now().UTC())

	// Audited with the state it replaced. "It was open, with 47 failures" is
	// what somebody asks about later; "it was reset" is not an answer.
	h.record(r.Context(), "breaker.reset", name,
		fmt.Sprintf("was %s; the channel will be tried again on the next delivery", was))

	writeJSON(w, http.StatusOK, map[string]any{"channel": name, "was": string(was), "state": "closed"})
}

type auditResponse struct {
	Actions []auditView `json:"actions"`
}

type auditView struct {
	At     time.Time `json:"at"`
	Actor  string    `json:"actor"`
	Action string    `json:"action"`
	Target string    `json:"target,omitempty"`
	Detail string    `json:"detail,omitempty"`
}

// listAudit implements GET /admin/api/audit.
func (h *handler) listAudit(w http.ResponseWriter, r *http.Request) {
	if h.deps.Audit == nil {
		writeJSON(w, http.StatusOK, auditResponse{})
		return
	}

	actions, err := h.deps.Audit.ListAdminActions(r.Context(), 200)
	if err != nil {
		h.log.Error("admin: listing audit actions failed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal", "the audit trail could not be read")
		return
	}

	out := make([]auditView, 0, len(actions))
	for _, a := range actions {
		out = append(out, auditView{At: a.At, Actor: a.Actor, Action: a.Action, Target: a.Target, Detail: a.Detail})
	}
	writeJSON(w, http.StatusOK, auditResponse{Actions: out})
}

// ------------------------------------------------------------------ helpers

// validateChannel builds the channel and checks its parameters against its own
// schema, which is what router.New does at startup.
//
// Doing it here means an operator finds out while looking at the form rather
// than after saving and restarting. It is the same two calls in the same order,
// deliberately: a validation that differed from the one that runs at boot would
// be worse than none.
func validateChannel(cfg config.ChannelConfig) error {
	if !channel.IsRegistered(cfg.Type) {
		return fmt.Errorf("unknown channel type %q (registered: %v)", cfg.Type, channel.Registered())
	}

	ch, err := channel.New(cfg.Type, cfg.Name, cfg.Config)
	if err != nil {
		return err
	}
	return channel.ValidateParams(cfg.Type, ch.ParamSchema(), cfg.Config)
}

// reload rebuilds the router from the stored configuration.
//
// Every write goes through here, so "the database is the truth" is not a claim
// about startup but a property of the running process: what is stored is what
// is delivering, one call after the change.
func (h *handler) reload(ctx context.Context) error {
	if h.deps.Router == nil {
		return nil
	}

	stored, err := h.deps.Channels.Load(ctx)
	if err != nil {
		return err
	}
	return h.deps.Router.Reload(stored)
}

// describeChange summarises an edit for the audit trail.
//
// It names the parameters that changed and never their values: the audit trail
// is read by more people than the configuration is, and "token changed" is the
// useful fact about a credential edit.
func describeChange(before, after config.ChannelConfig) string {
	var changed []string

	if before.Type != after.Type {
		changed = append(changed, "type")
	}
	if before.IsEnabled() != after.IsEnabled() {
		changed = append(changed, "enabled")
	}
	if before.Quota != after.Quota {
		changed = append(changed, "quota")
	}

	keys := map[string]bool{}
	for k := range before.Config {
		keys[k] = true
	}
	for k := range after.Config {
		keys[k] = true
	}
	for k := range keys {
		if fmt.Sprint(before.Config[k]) != fmt.Sprint(after.Config[k]) {
			changed = append(changed, k)
		}
	}

	if len(changed) == 0 {
		return "saved with no changes"
	}
	return "changed: " + join(changed)
}

func join(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	return out
}
