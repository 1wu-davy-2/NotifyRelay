package admin

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"notifyrelay/internal/store"
)

// DeliveryStore is what the operator surface needs from delivery state.
//
// Narrower than store.Store, and deliberately so: the management UI reads
// deliveries and replays dead letters, and it has no business claiming,
// completing or pruning anything. An interface is the cheapest way to say that
// and the only one a reviewer can check at a glance.
type DeliveryStore interface {
	Get(ctx context.Context, id string) (*store.Delivery, error)
	List(ctx context.Context, f store.Filter) ([]*store.Delivery, error)
	Attempts(ctx context.Context, deliveryID string) ([]*store.Attempt, error)
	Replay(ctx context.Context, id string, now time.Time) (bool, error)
	Stats(ctx context.Context) (store.Stats, error)
}

// BodyStore reports whether a delivery's message is still on disk.
//
// The dead-letter row and the message body have separate lifetimes — the row is
// kept for the retention window, the body is a file the pruner collects by age
// — so "this delivery can be replayed" is a question about the file, not about
// the row. Without asking it, a replay of an expired body fails a second later
// with "payload missing" and looks like the button is broken.
type BodyStore interface {
	Get(id string) ([]byte, error)
}

// Waker nudges the queue after a replay, so the delivery goes out now rather
// than at the next poll. Optional: without it the replay still works, one poll
// interval later.
type Waker interface {
	Wake()
}

// deliveryView is one delivery as the UI sees it.
type deliveryView struct {
	ID            string     `json:"id"`
	RequestID     string     `json:"request_id"`
	Target        string     `json:"target"`
	ChannelType   string     `json:"channel_type"`
	Status        string     `json:"status"`
	Attempts      int        `json:"attempts"`
	LastError     string     `json:"last_error,omitempty"`
	LastClass     string     `json:"last_class,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	NextAttemptAt time.Time  `json:"next_attempt_at,omitempty"`
	SentAt        *time.Time `json:"sent_at,omitempty"`
	// Replayable is computed, not stored: it depends on the status and on
	// whether the message body is still on disk, and a UI that offered the
	// button for a delivery whose body is gone would be offering a failure.
	Replayable bool `json:"replayable"`
}

type attemptView struct {
	AttemptNo   int       `json:"attempt_no"`
	Class       string    `json:"class"`
	SkipReason  string    `json:"skip_reason,omitempty"`
	Detail      string    `json:"detail,omitempty"`
	Error       string    `json:"error,omitempty"`
	ElapsedMS   int64     `json:"elapsed_ms"`
	CreatedAt   time.Time `json:"created_at"`
	ChannelType string    `json:"channel_type"`
	Target      string    `json:"target"`
}

func (h *handler) viewDelivery(d *store.Delivery) deliveryView {
	return deliveryView{
		ID: d.ID, RequestID: d.RequestID, Target: d.Target, ChannelType: d.ChannelType,
		Status: string(d.Status), Attempts: d.Attempts,
		LastError: d.LastError, LastClass: d.LastClass,
		CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
		NextAttemptAt: d.NextAttemptAt, SentAt: d.SentAt,
		Replayable: d.Status.Replayable() && h.bodyAvailable(d.ID),
	}
}

// bodyAvailable reports whether a delivery's message is still readable.
func (h *handler) bodyAvailable(id string) bool {
	if h.deps.Bodies == nil {
		return false
	}
	_, err := h.deps.Bodies.Get(id)
	return err == nil
}

// listDeliveries implements GET /admin/api/deliveries.
func (h *handler) listDeliveries(w http.ResponseWriter, r *http.Request) {
	if h.deps.Deliveries == nil {
		writeJSON(w, http.StatusOK, map[string]any{"deliveries": []deliveryView{}})
		return
	}

	query := r.URL.Query()
	filter := store.Filter{
		Status:    store.Status(query.Get("status")),
		Target:    query.Get("target"),
		RequestID: query.Get("request_id"),
	}

	for _, p := range []struct {
		name string
		into *int
	}{{"limit", &filter.Limit}, {"offset", &filter.Offset}} {
		raw := query.Get(p.name)
		if raw == "" {
			continue
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid_request", p.name+" must be a non-negative integer")
			return
		}
		*p.into = n
	}

	deliveries, err := h.deps.Deliveries.List(r.Context(), filter)
	if err != nil {
		h.log.Error("admin: listing deliveries failed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal", "the deliveries could not be read")
		return
	}

	out := make([]deliveryView, 0, len(deliveries))
	for _, d := range deliveries {
		out = append(out, h.viewDelivery(d))
	}
	writeJSON(w, http.StatusOK, map[string]any{"deliveries": out})
}

// getDelivery implements GET /admin/api/deliveries/{id}.
func (h *handler) getDelivery(w http.ResponseWriter, r *http.Request) {
	if h.deps.Deliveries == nil {
		writeError(w, http.StatusNotImplemented, "unavailable", "this deployment has no delivery store")
		return
	}

	id := chi.URLParam(r, "id")
	d, err := h.deps.Deliveries.Get(r.Context(), id)
	if err != nil {
		h.log.Error("admin: reading a delivery failed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal", "the delivery could not be read")
		return
	}
	if d == nil {
		writeError(w, http.StatusNotFound, "not_found", "no delivery with that id")
		return
	}

	attempts, err := h.deps.Deliveries.Attempts(r.Context(), id)
	if err != nil {
		h.log.Error("admin: reading attempts failed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal", "the attempt history could not be read")
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

	writeJSON(w, http.StatusOK, map[string]any{
		"delivery": h.viewDelivery(d),
		"attempts": views,
	})
}

// replayDelivery implements POST /admin/api/deliveries/{id}/replay.
func (h *handler) replayDelivery(w http.ResponseWriter, r *http.Request) {
	if h.deps.Deliveries == nil {
		writeError(w, http.StatusNotImplemented, "unavailable", "this deployment has no delivery store")
		return
	}

	id := chi.URLParam(r, "id")

	d, err := h.deps.Deliveries.Get(r.Context(), id)
	if err != nil {
		h.log.Error("admin: reading a delivery failed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal", "the delivery could not be read")
		return
	}
	if d == nil {
		writeError(w, http.StatusNotFound, "not_found", "no delivery with that id")
		return
	}

	// Refused before the store is asked, so the operator gets the reason rather
	// than a delivery that fails again a second later.
	if !d.Status.Replayable() {
		writeError(w, http.StatusConflict, "not_replayable",
			"only a dead-lettered delivery can be replayed; this one is "+string(d.Status))
		return
	}
	if !h.bodyAvailable(id) {
		writeError(w, http.StatusConflict, "body_expired",
			"the message body is no longer on disk, so there is nothing to send; "+
				"it was removed by the retention policy")
		return
	}

	replayed, err := h.deps.Deliveries.Replay(r.Context(), id, time.Now().UTC())
	if err != nil {
		h.log.Error("admin: replay failed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal", "the delivery could not be replayed")
		return
	}
	if !replayed {
		// The row changed between the read and the write: something else
		// replayed it, or a worker picked it up. Not an error worth a 500.
		writeError(w, http.StatusConflict, "not_replayable",
			"the delivery is no longer in a state that can be replayed")
		return
	}

	h.record(r.Context(), "delivery.replay", d.Target,
		"replayed delivery "+id+" (request "+d.RequestID+")")

	if h.deps.Waker != nil {
		h.deps.Waker.Wake()
	}

	writeJSON(w, http.StatusOK, map[string]any{"replayed": true, "id": id})
}

// stats implements GET /admin/api/stats.
func (h *handler) stats(w http.ResponseWriter, r *http.Request) {
	if h.deps.Deliveries == nil {
		writeJSON(w, http.StatusOK, store.Stats{})
		return
	}

	s, err := h.deps.Deliveries.Stats(r.Context())
	if err != nil {
		h.log.Error("admin: reading queue stats failed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal", "the queue statistics could not be read")
		return
	}
	writeJSON(w, http.StatusOK, s)
}

// ErrNoBody is returned by a BodyStore that cannot find a message.
var ErrNoBody = errors.New("admin: no message body")
