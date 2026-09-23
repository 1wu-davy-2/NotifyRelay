package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"notifyrelay/internal/channel"
	"notifyrelay/internal/message"
	"notifyrelay/internal/queue"
	"notifyrelay/internal/recipients"
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
// The caller describes WHAT happened, and — for a channel that delivers to an
// address the caller knows and the operator does not — WHO it is for. It never
// describes how to reach the peer: the SMTP host, the token, the webhook URL
// all live in the channel configuration.
//
// `to` is the second half of that. A registration mail has a recipient that
// exists only in the request, so a channel's fixed recipient list cannot
// express it; without this field the whole category of transactional mail is
// unsendable, and with it the relay would mail anyone unless the key's own
// allow list says otherwise (internal/recipients).
type notifyRequest struct {
	Targets []string `json:"targets"`
	// To names recipients for this request, for channels that address them.
	//
	// It applies to every target, which is why mixing it with a target that
	// does not take recipients is refused rather than quietly ignored.
	To       []string       `json:"to"`
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
	ID string `json:"id"`
	// Target is the reference the caller wrote, echoed back so that the reply
	// lines up with the request.
	Target string `json:"target"`
	// Channel is the instance it resolved to. Both are here because they answer
	// different questions: which string the caller sent, and which channel is
	// actually doing the work — the second is what an operator looks up in the
	// delivery list.
	Channel     string `json:"channel"`
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

	targets, targetErrs := h.deliveryTargets(ctx, &req)

	// A request-level refusal — a recipient this key may not address, or a
	// target that already names its own — is answered the same way on both
	// paths. It is not a fact about the target, and the synchronous path's
	// habit of reporting trouble per target would disguise an authorisation
	// failure as a delivery failure.
	for _, err := range targetErrs {
		if status, code, msg, ok := requestRefusal(err); ok {
			return requestID, status, errorBody{Error: code, Message: msg}
		}
	}

	h.log.Info("notify",
		slog.String("request_id", requestID),
		slog.Int("targets", len(req.Targets)),
		slog.String("type", string(msg.Type)),
		slog.Bool("sync", req.Sync),
	)

	if req.Sync || h.queue == nil {
		// Targets that resolved are delivered; the rest become results of their
		// own. Reporting them per target rather than as a status code is the
		// synchronous contract: one unresolvable target must not hide the
		// outcome of the others.
		results := make([]router.TargetResult, len(req.Targets))
		deliverable := make([]channel.Target, 0, len(req.Targets))
		at := make([]int, 0, len(req.Targets))
		for i, err := range targetErrs {
			if err != nil {
				results[i] = router.PermanentFailure(req.Targets[i], err)
				continue
			}
			deliverable = append(deliverable, targets[i])
			at = append(at, i)
		}

		// DeliverAll preserves input order, so each result lands back in the
		// slot its target came from.
		for j, res := range h.router.DeliverAll(ctx, requestID, deliverable, msg) {
			results[at[j]] = res
		}

		// Always 200 for a request that was understood. The per-target results
		// carry the outcome; an HTTP status that summarised them would have to
		// pick one target's fate to represent all of them.
		return requestID, http.StatusOK, notifyResponse{RequestID: requestID, Results: results}
	}

	return h.accept(ctx, requestID, msg, targets, targetErrs)
}

// apiError is a refusal that is about the request rather than about the
// deployment: the status and the error code the caller should be given.
//
// It travels as an error so that resolving targets stays a single pass, while
// the decision of what to do about it stays with the handler.
type apiError struct {
	status int
	code   string
	msg    string
}

func (e *apiError) Error() string { return e.msg }

// requestRefusal reports whether an error is about the *request* rather than
// about the target, and what the caller should be told.
//
// These are the failures that both paths answer the same way, before the split
// into synchronous results and a queued acknowledgement.
func requestRefusal(err error) (status int, code, msg string, ok bool) {
	var refused *apiError
	if errors.As(err, &refused) {
		return refused.status, refused.code, refused.msg, true
	}

	var conflict *router.RecipientConflictError
	if errors.As(err, &conflict) {
		return http.StatusBadRequest, "invalid_request", conflict.Error(), true
	}

	return 0, "", "", false
}

// deliveryTargets resolves every target and applies the caller's allow list.
//
// One entry per target, with the reason where one did not resolve, because the
// two paths report failures differently: the asynchronous one refuses the whole
// request, the synchronous one reports each target's fate separately and must
// still deliver to the rest.
func (h *notifyHandler) deliveryTargets(ctx context.Context, req *notifyRequest) ([]channel.Target, []error) {
	targets := make([]channel.Target, len(req.Targets))
	errs := make([]error, len(req.Targets))

	for i, ref := range req.Targets {
		t, _, err := h.router.ResolveTarget(ref, req.To)
		if err != nil {
			errs[i] = err
			continue
		}
		targets[i] = t
	}

	// Applied to the recipients of every target, whoever named them: the `to`
	// field and a mailto: URL are two spellings of the same thing, and checking
	// only the first would leave the second as a way round the list.
	//
	// A missing identity fails closed on its own — the zero Identity allows
	// nothing — which is what a route that forgot the middleware deserves.
	id, _ := identityFrom(ctx)
	for i := range targets {
		if errs[i] != nil {
			continue
		}
		for _, addr := range targets[i].Recipients {
			if !recipients.Allows(id.AllowedRecipients, addr) {
				errs[i] = &apiError{
					status: http.StatusForbidden,
					code:   "recipient_not_allowed",
					msg: fmt.Sprintf("API key %q may not send to %q (see the key's allowed_recipients)",
						id.Name, addr),
				}
				break
			}
		}
	}

	return targets, errs
}

// targetError maps a target that did not resolve to the error code the caller
// acts on.
//
// The distinction is worth a type: "no channel is called that" sends the caller
// to its configuration, while "this channel has no recipients" tells it that it
// addressed the wrong kind of target.
func targetError(err error) errorBody {
	var policy *router.RecipientPolicyError
	if errors.As(err, &policy) {
		return errorBody{Error: "recipients_not_supported", Message: err.Error()}
	}
	return errorBody{Error: "unknown_target", Message: err.Error()}
}

// accept validates the targets and queues the deliveries.
func (h *notifyHandler) accept(ctx context.Context, requestID string, msg *message.Message, targets []channel.Target, errs []error) (string, int, any) {
	specs := make([]queue.TargetSpec, 0, len(targets))
	for i, t := range targets {
		if errs[i] != nil {
			// Rejected now rather than queued and failed later: an
			// acknowledgement the caller cannot act on is worse than a refusal.
			return requestID, http.StatusBadRequest, targetError(errs[i])
		}
		specs = append(specs, queue.TargetSpec{
			Target: t.Ref,
			// Pinned here, at the moment the configuration is known to be the
			// one the caller was answered against. The worker must not resolve
			// it again: "the only email channel" is a fact about now.
			Channel:     t.Instance,
			Recipients:  t.Recipients,
			ChannelType: h.router.TypeOf(t.Instance),
		})
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
			Channel:     d.Channel,
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
