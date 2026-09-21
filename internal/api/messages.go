package api

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"notifyrelay/internal/store"
)

type messageHandler struct {
	store DeliveryReader
	log   *slog.Logger
}

type messagesHandler struct {
	store DeliveryReader
	log   *slog.Logger
}

// DeliveryReader reads deliveries and their attempt history.
type DeliveryReader interface {
	Get(ctx context.Context, id string) (*store.Delivery, error)
	List(ctx context.Context, f store.Filter) ([]*store.Delivery, error)
	Attempts(ctx context.Context, deliveryID string) ([]*store.Attempt, error)
}

type deliveryView struct {
	ID          string     `json:"id"`
	RequestID   string     `json:"request_id"`
	Target      string     `json:"target"`
	ChannelType string     `json:"channel_type"`
	Status      string     `json:"status"`
	Attempts    int        `json:"attempts"`
	LastError   string     `json:"last_error,omitempty"`
	LastClass   string     `json:"last_class,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	NextAttemptAt time.Time `json:"next_attempt_at,omitempty"`
	SentAt      *time.Time `json:"sent_at,omitempty"`
}

type attemptView struct {
	AttemptNo  int       `json:"attempt_no"`
	Class      string    `json:"class"`
	Detail     string    `json:"detail,omitempty"`
	Error      string    `json:"error,omitempty"`
	ElapsedMS  int64     `json:"elapsed_ms"`
	CreatedAt  time.Time `json:"created_at"`
	ChannelType string   `json:"channel_type"`
	Target     string    `json:"target"`
}

type messageDetail struct {
	Delivery deliveryView  `json:"delivery"`
	Attempts []attemptView `json:"attempts"`
}

type messageList struct {
	Deliveries []deliveryView `json:"deliveries"`
}

func viewDelivery(d *store.Delivery) deliveryView {
	return deliveryView{
		ID: d.ID, RequestID: d.RequestID, Target: d.Target, ChannelType: d.ChannelType,
		Status: string(d.Status), Attempts: d.Attempts,
		LastError: d.LastError, LastClass: d.LastClass,
		CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
		NextAttemptAt: d.NextAttemptAt, SentAt: d.SentAt,
	}
}

func viewAttempts(attempts []*store.Attempt) []attemptView {
	out := make([]attemptView, 0, len(attempts))
	for _, a := range attempts {
		out = append(out, attemptView{
			AttemptNo: a.AttemptNo, Class: a.Class, Detail: a.Detail, Error: a.Error,
			ElapsedMS: a.ElapsedMS, CreatedAt: a.CreatedAt,
			ChannelType: a.ChannelType, Target: a.Target,
		})
	}
	return out
}

// messageHandler serves one delivery with its history.
func (h *messageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		WriteError(w, http.StatusBadRequest, "invalid_request", "a delivery id is required")
		return
	}

	d, err := h.store.Get(r.Context(), id)
	if err != nil {
		h.log.Error("api: reading a delivery failed", "error", err)
		WriteError(w, http.StatusInternalServerError, "internal", "the delivery could not be read")
		return
	}
	if d == nil {
		WriteError(w, http.StatusNotFound, "not_found", "no delivery with that id")
		return
	}

	attempts, err := h.store.Attempts(r.Context(), id)
	if err != nil {
		h.log.Error("api: reading attempts failed", "error", err)
		WriteError(w, http.StatusInternalServerError, "internal", "the attempt history could not be read")
		return
	}

	WriteJSON(w, http.StatusOK, messageDetail{
		Delivery: viewDelivery(d),
		Attempts: viewAttempts(attempts),
	})
}

// messagesHandler lists deliveries, newest first.
func (h *messagesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	filter := store.Filter{
		Status:    store.Status(query.Get("status")),
		Target:    query.Get("target"),
		RequestID: query.Get("request_id"),
	}

	if raw := query.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			WriteError(w, http.StatusBadRequest, "invalid_request", "limit must be a non-negative integer")
			return
		}
		filter.Limit = n
	}
	if raw := query.Get("offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			WriteError(w, http.StatusBadRequest, "invalid_request", "offset must be a non-negative integer")
			return
		}
		filter.Offset = n
	}

	deliveries, err := h.store.List(r.Context(), filter)
	if err != nil {
		h.log.Error("api: listing deliveries failed", "error", err)
		WriteError(w, http.StatusInternalServerError, "internal", "deliveries could not be listed")
		return
	}

	out := make([]deliveryView, 0, len(deliveries))
	for _, d := range deliveries {
		out = append(out, viewDelivery(d))
	}

	WriteJSON(w, http.StatusOK, messageList{Deliveries: out})
}
