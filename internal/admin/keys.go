package admin

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"notifyrelay/internal/admin/i18n"
	"notifyrelay/internal/auth"
	"notifyrelay/internal/recipients"
	"notifyrelay/internal/requestid"
	"notifyrelay/internal/store"
)

// API keys, managed from the operator surface.
//
// They live in the database so that creating one does not mean editing a file
// and restarting the service. The plaintext is shown exactly once, in the
// response that creates it: only the digest is stored, so nothing — not the
// operator, not the database, not this service — can produce it again. That is
// the same property the configuration file's key_hash has, and it is why the
// create response is the only place a token ever appears.

// keyPrefix marks a generated token as one of ours.
//
// It makes a leaked token recognisable in a log, a paste, or a secret scanner's
// output — which is worth the few bytes, and costs nothing.
const keyPrefix = "nr_"

// keyTokenBytes is the entropy behind a generated token. 32 bytes is the same
// size as the digest it produces, and guessing it is not a threat model anybody
// has to think about again.
const keyTokenBytes = 32

// keyView is a key as the UI sees it. There is deliberately no field for the
// token: this struct is built from the stored row, which does not have one.
type keyView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"created_at"`
	// LastUsedAt is empty until the key is first used.
	LastUsedAt string `json:"last_used_at,omitempty"`
	// Source says where the key came from, so an operator looking at a list can
	// tell which entries they can delete here and which are pinned by the
	// configuration file.
	Source string `json:"source"`
	// AllowedRecipients is the address patterns this key may name as a
	// recipient of a notification. Empty is the default and means none: the
	// key can only reach the destinations a channel was configured with.
	AllowedRecipients []string `json:"allowed_recipients,omitempty"`
}

type saveKeyRequest struct {
	Name string `json:"name"`
	// Enabled and AllowedRecipients are pointers so that "absent" is
	// distinguishable from "empty".
	//
	// For a boolean the two mean the same thing, but for the list they do not:
	// absent means "leave it alone" and empty means "this key may address
	// nobody". Without the distinction, toggling a key off and on again would
	// silently revoke its recipients — the sort of change nobody notices until
	// a password reset stops arriving.
	Enabled           *bool     `json:"enabled"`
	AllowedRecipients *[]string `json:"allowed_recipients"`
}

// allowedRecipients returns the requested patterns and whether any were
// requested, refusing a malformed one before it is stored.
//
// Refused here rather than at the first notification because this is the moment
// somebody can still see what they typed. A pattern that matches nothing is the
// worst outcome: the key looks configured and quietly fails.
func (req saveKeyRequest) allowedRecipients(t *i18n.Messages) ([]string, bool, string) {
	if req.AllowedRecipients == nil {
		return nil, false, ""
	}
	for _, pattern := range *req.AllowedRecipients {
		if err := recipients.Validate(pattern); err != nil {
			return nil, false, fmt.Sprintf(t.ErrKeyRecipientPattern, pattern)
		}
	}
	return *req.AllowedRecipients, true, ""
}

// keysPage implements GET /admin/keys.
func (h *handler) keysPage(w http.ResponseWriter, r *http.Request, actor string) {
	views, err := h.keyViews(r)
	if err != nil {
		h.renderError(w, r, actor, "keys", copyFor(r).ErrKeysUnreadable, err)
		return
	}

	h.render(w, r, "keys.html", struct {
		pageData
		Keys []keyView
		// Configured is how many keys come from the configuration file. Shown
		// because those cannot be deleted here, and a list that silently omits
		// the reason is a list somebody will try to edit.
		Configured int
	}{
		pageData:   h.pageBase(r, actor, "keys"),
		Keys:       views,
		Configured: len(h.configuredKeyNames()),
	})
}

// listKeys implements GET /admin/api/keys.
func (h *handler) listKeys(w http.ResponseWriter, r *http.Request) {
	t := copyFor(r)

	views, err := h.keyViews(r)
	if err != nil {
		h.log.Error("admin: listing API keys failed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal", t.ErrKeysUnreadable)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": views})
}

// createKey implements POST /admin/api/keys.
//
// The response is the only time the token exists outside the caller's memory.
func (h *handler) createKey(w http.ResponseWriter, r *http.Request) {
	t := copyFor(r)

	if h.deps.Keys == nil {
		writeError(w, http.StatusNotImplemented, "unavailable", t.ErrNoKeyStore)
		return
	}

	var req saveKeyRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", t.ErrInvalidJSON)
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", t.ErrKeyNameRequired)
		return
	}

	token, err := generateToken()
	if err != nil {
		h.log.Error("admin: could not generate an API key", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal", t.ErrKeyCreateFailed)
		return
	}

	allowed, _, refusal := req.allowedRecipients(t)
	if refusal != "" {
		writeError(w, http.StatusBadRequest, "invalid_request", refusal)
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	key := &store.APIKey{
		ID:                requestid.New(),
		Name:              name,
		KeyHash:           auth.HashAPIKey(token),
		Enabled:           enabled,
		AllowedRecipients: allowed,
		CreatedAt:         time.Now().UTC(),
	}
	if err := h.deps.Keys.PutAPIKey(r.Context(), key); err != nil {
		h.log.Error("admin: could not store an API key", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal", t.ErrKeyCreateFailed)
		return
	}

	// The name, never the token. An audit trail is read by more people than the
	// create response is, and it is kept far longer.
	h.record(r.Context(), "key.create", name, "created")

	writeJSON(w, http.StatusOK, map[string]any{
		"id":      key.ID,
		"name":    key.Name,
		"token":   token,
		"warning": t.ErrTokenOnce,
	})
}

// updateKey implements POST /admin/api/keys/{id}.
//
// Enable, disable, and the address allow list. The token itself cannot be
// changed: rotating means creating a new key and deleting the old one, which is
// the operation that leaves an overlap during which both work — and doing it
// any other way would mean a rotation with an outage in the middle.
//
// Both fields are optional and an absent one is left alone. That is not
// leniency for its own sake: the toggle and the allow list are edited in
// different places, and requiring both on every call would make flipping a key
// off and on again silently rewrite a list the caller never saw.
func (h *handler) updateKey(w http.ResponseWriter, r *http.Request) {
	t := copyFor(r)

	if h.deps.Keys == nil {
		writeError(w, http.StatusNotImplemented, "unavailable", t.ErrNoKeyStore)
		return
	}

	id := chi.URLParam(r, "id")

	var req saveKeyRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", t.ErrInvalidJSON)
		return
	}

	key, err := h.deps.Keys.GetAPIKey(r.Context(), id)
	if err != nil {
		h.log.Error("admin: reading an API key failed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal", t.ErrKeyUnreadable)
		return
	}
	if key == nil {
		writeError(w, http.StatusNotFound, "not_found", t.ErrKeyNotFound)
		return
	}

	allowed, given, refusal := req.allowedRecipients(t)
	if refusal != "" {
		writeError(w, http.StatusBadRequest, "invalid_request", refusal)
		return
	}

	if req.Enabled != nil {
		key.Enabled = *req.Enabled
	}
	if given {
		key.AllowedRecipients = allowed
	}
	if err := h.deps.Keys.PutAPIKey(r.Context(), key); err != nil {
		h.log.Error("admin: updating an API key failed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal", t.ErrKeyUpdateFailed)
		return
	}

	state := "disabled"
	if key.Enabled {
		state = "enabled"
	}
	h.record(r.Context(), "key.update", key.Name, state)

	writeJSON(w, http.StatusOK, map[string]any{"id": key.ID, "enabled": key.Enabled})
}

// deleteKey implements DELETE /admin/api/keys/{id}.
func (h *handler) deleteKey(w http.ResponseWriter, r *http.Request) {
	t := copyFor(r)

	if h.deps.Keys == nil {
		writeError(w, http.StatusNotImplemented, "unavailable", t.ErrNoKeyStore)
		return
	}

	id := chi.URLParam(r, "id")

	key, err := h.deps.Keys.GetAPIKey(r.Context(), id)
	if err != nil {
		h.log.Error("admin: reading an API key failed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal", t.ErrKeyUnreadable)
		return
	}
	if key == nil {
		writeError(w, http.StatusNotFound, "not_found", t.ErrKeyNotFound)
		return
	}

	deleted, err := h.deps.Keys.DeleteAPIKey(r.Context(), id)
	if err != nil {
		h.log.Error("admin: deleting an API key failed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal", t.ErrKeyDeleteFailed)
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, "not_found", t.ErrKeyNotFound)
		return
	}

	h.record(r.Context(), "key.delete", key.Name, "deleted")

	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// keyViews lists stored keys and the ones named in the configuration.
//
// Both are shown, because an operator looking at this page is asking "which
// credentials can reach this service" — and answering with only the half that
// happens to live in the database would be a list that is wrong in the
// direction that matters.
func (h *handler) keyViews(r *http.Request) ([]keyView, error) {
	views := make([]keyView, 0, 8)

	for _, k := range h.deps.Auth.APIKeys {
		views = append(views, keyView{
			ID:     "config:" + k.Name,
			Name:   k.Name,
			Source: "configuration",
			// A key in the file is enabled by the file. The UI cannot change it
			// and does not pretend to.
			Enabled:           true,
			AllowedRecipients: k.AllowedRecipients,
		})
	}

	if h.deps.Keys == nil {
		return views, nil
	}

	stored, err := h.deps.Keys.ListAPIKeys(r.Context())
	if err != nil {
		return nil, err
	}
	for _, k := range stored {
		v := keyView{
			ID:                k.ID,
			Name:              k.Name,
			Enabled:           k.Enabled,
			CreatedAt:         k.CreatedAt.Format(time.RFC3339),
			Source:            "database",
			AllowedRecipients: k.AllowedRecipients,
		}
		if k.LastUsedAt != nil {
			v.LastUsedAt = k.LastUsedAt.Format(time.RFC3339)
		}
		views = append(views, v)
	}

	return views, nil
}

// configuredKeyNames returns the names of keys defined in the configuration.
func (h *handler) configuredKeyNames() []string {
	names := make([]string, 0, len(h.deps.Auth.APIKeys))
	for _, k := range h.deps.Auth.APIKeys {
		names = append(names, k.Name)
	}
	return names
}

// generateToken returns a new API token.
func generateToken() (string, error) {
	buf := make([]byte, keyTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("admin: reading random bytes: %w", err)
	}
	// URL-safe and unpadded, so the token survives being put in a header, a
	// query string, a shell variable or a YAML value without quoting games.
	return keyPrefix + base64.RawURLEncoding.EncodeToString(buf), nil
}
