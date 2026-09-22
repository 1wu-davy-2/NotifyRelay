package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"notifyrelay/internal/message"
	"notifyrelay/internal/queue"
	"notifyrelay/internal/requestid"
	"notifyrelay/internal/router"
	"notifyrelay/internal/store"
)

// maxBodyBytes caps a request body. A notification is text; anything larger is
// a mistake or an attack, and neither should be read into memory.
const maxBodyBytes = 1 << 20 // 1 MiB

// idempotencyHeader is the header a caller uses to make a retry safe.
const idempotencyHeader = "Idempotency-Key"

// notifyRequest is the wire format of POST /api/v1/notify.
//
// The caller describes WHAT happened. It never describes how to deliver it —
// that lives in the channel configuration.
type notifyRequest struct {
	Targets  []string       `json:"targets"`
	Title    string         `json:"title"`
	Body     string         `json:"body"`
	Format   string         `json:"format"`   // text | markdown | html
	Type     string         `json:"type"`     // info | success | warning | failure
	Priority int            `json:"priority"` // 1-5
	Tags     []string       `json:"tags"`
	Links    []linkPayload  `json:"links"`
	At       []string       `json:"at"`
	Meta     map[string]any `json:"meta"`

	// Sync waits for the deliveries to finish and returns their outcomes.
	//
	// Asynchronous is the default: a caller that wants an answer now is
	// choosing to wait for a channel that may be rate-limited, and the queue
	// exists precisely so that it does not have to.
	Sync bool `json:"sync"`
}

type linkPayload struct {
	Text string `json:"text"`
	URL  string `json:"url"`
}

// notifyResponse is the synchronous result: one outcome per target.
//
// There is deliberately no single overall status. Collapsing a fan-out into
// one word is exactly how a partial failure gets hidden.
type notifyResponse struct {
	RequestID string                `json:"request_id"`
	Results   []router.TargetResult `json:"results"`
}

// acceptedResponse is the asynchronous result: what was queued.
type acceptedResponse struct {
	RequestID  string           `json:"request_id"`
	Accepted   bool             `json:"accepted"`
	Deliveries []deliveryPayload `json:"deliveries"`
}

type deliveryPayload struct {
	ID          string `json:"id"`
	Target      string `json:"target"`
	ChannelType string `json:"channel_type"`
	Status      string `json:"status"`
}

// Enqueuer accepts deliveries for asynchronous sending.
type Enqueuer interface {
	Enqueue(ctx context.Context, requestID string, msg *message.Message, targets []queue.TargetSpec) ([]*store.Delivery, error)
}

// IdempotencyStore replays responses for repeated requests.
type IdempotencyStore interface {
	GetRecord(ctx context.Context, key string) (*store.Record, error)
	PutRecord(ctx context.Context, rec *store.Record) error
}

type notifyHandler struct {
	router  *router.Router
	queue   Enqueuer
	records IdempotencyStore
	log     *slog.Logger
	live    *Live
}

// ServeHTTP implements POST /api/v1/notify.
func (h *notifyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), h.live.HandlerTimeout())
	defer cancel()

	key := r.Header.Get(idempotencyHeader)
	if key != "" {
		if rec, err := h.records.GetRecord(ctx, key); err == nil && rec != nil {
			// The same key gets the same answer, byte for byte. A caller
			// retrying a request it never saw the response to wants the
			// original response, not an acknowledgement that something
			// happened once.
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Idempotent-Replay", "true")
			w.WriteHeader(rec.Status)
			_, _ = w.Write(rec.Body)
			return
		}
	}

	requestID, status, body := h.handle(ctx, w, r)

	// Serialize once and use the same bytes for the response and for the
	// stored replay. Encoding twice — once to answer, once to remember —
	// produces two bodies that differ in whatever the encoder does
	// incidentally, and a replayed response that is not byte-identical to the
	// original is not a replay.
	encoded, err := json.Marshal(body)
	if err != nil {
		h.log.Error("api: could not encode the response", slog.String("error", err.Error()))
		WriteError(w, http.StatusInternalServerError, "internal", "the response could not be encoded")
		return
	}

	// Built in one step rather than appended to the marshalled bytes, so the
	// slice handed to the store cannot be the same array the response is
	// written from.
	response := make([]byte, 0, len(encoded)+1)
	response = append(response, encoded...)
	response = append(response, '\n')

	if key != "" && (status == http.StatusOK || status == http.StatusAccepted) {
		if err := h.records.PutRecord(ctx, &store.Record{
			Key: key, RequestID: requestID, Status: status, Body: response, CreatedAt: time.Now().UTC(),
		}); err != nil {
			h.log.Warn("api: could not store an idempotency record", slog.String("error", err.Error()))
		}
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(response)
}

// handle does the work and reports the request id, the status to send and the
// body to send with it.
func (h *notifyHandler) handle(ctx context.Context, w http.ResponseWriter, r *http.Request) (string, int, any) {
	var req notifyRequest

	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return "", http.StatusBadRequest,
			errorBody{Error: "invalid_request", Message: "request body is not valid JSON: " + err.Error()}
	}

	msg, err := req.toMessage()
	if err != nil {
		return "", http.StatusBadRequest,
			errorBody{Error: "invalid_request", Message: err.Error()}
	}

	if len(req.Targets) == 0 {
		return "", http.StatusBadRequest,
			errorBody{Error: "invalid_request", Message: "targets must list at least one channel"}
	}

	requestID := requestid.New()

	h.log.Info("notify",
		slog.String("request_id", requestID),
		slog.Int("targets", len(req.Targets)),
		slog.String("type", string(msg.Type)),
		slog.Bool("sync", req.Sync),
	)

	if req.Sync || h.queue == nil {
		results := h.router.DeliverAll(ctx, requestID, req.Targets, msg)
		// Always 200 for a request that was understood. The per-target results
		// carry the outcome; an HTTP status that summarised them would have to
		// pick one target's fate to represent all of them.
		return requestID, http.StatusOK, notifyResponse{RequestID: requestID, Results: results}
	}

	return h.accept(ctx, requestID, msg, req.Targets)
}

// accept validates the targets and queues the deliveries.
func (h *notifyHandler) accept(ctx context.Context, requestID string, msg *message.Message, targets []string) (string, int, any) {
	specs := make([]queue.TargetSpec, 0, len(targets))
	for _, t := range targets {
		alias, channelType, err := h.router.TargetType(t)
		if err != nil {
			// Rejected now rather than queued and failed later: an
			// acknowledgement the caller cannot act on is worse than a refusal.
			return requestID, http.StatusBadRequest,
				errorBody{Error: "unknown_target", Message: err.Error()}
		}
		specs = append(specs, queue.TargetSpec{Target: alias, ChannelType: channelType})
	}

	deliveries, err := h.queue.Enqueue(ctx, requestID, msg, specs)
	if err != nil {
		h.log.Error("api: enqueue failed", slog.String("error", err.Error()))
		return requestID, http.StatusServiceUnavailable,
			errorBody{Error: "queue_unavailable", Message: "the delivery could not be queued"}
	}

	out := make([]deliveryPayload, 0, len(deliveries))
	for _, d := range deliveries {
		out = append(out, deliveryPayload{
			ID:          d.ID,
			Target:      d.Target,
			ChannelType: d.ChannelType,
			Status:      string(d.Status),
		})
	}

	return requestID, http.StatusAccepted,
		acceptedResponse{RequestID: requestID, Accepted: true, Deliveries: out}
}

// toMessage converts and validates the request into the unified model.
func (r *notifyRequest) toMessage() (*message.Message, error) {
	format, err := message.ParseFormat(r.Format)
	if err != nil {
		return nil, err
	}
	typ, err := message.ParseType(r.Type)
	if err != nil {
		return nil, err
	}

	msg := &message.Message{
		Title:    r.Title,
		Body:     r.Body,
		Format:   format,
		Type:     typ,
		Priority: message.Priority(r.Priority),
		Tags:     r.Tags,
		At:       r.At,
		Meta:     r.Meta,
	}
	for _, l := range r.Links {
		msg.Links = append(msg.Links, message.Link{Text: l.Text, URL: l.URL})
	}

	msg.Normalize()
	if err := msg.Validate(); err != nil {
		return nil, err
	}
	return msg, nil
}
