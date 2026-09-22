package admin

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"notifyrelay/internal/message"
	"notifyrelay/internal/queue"
	"notifyrelay/internal/requestid"
	"notifyrelay/internal/store"
)

// Enqueuer accepts a delivery for asynchronous sending.
//
// Declared here rather than imported from internal/api even though the shape is
// identical. The two surfaces share a queue, not a contract: admin importing api
// would make it easy to reach for one of api's other exports by mistake, and the
// package comment says why the two are kept apart — they are different trust
// boundaries, authenticated by different things.
type Enqueuer interface {
	Enqueue(ctx context.Context, requestID string, msg *message.Message, targets []queue.TargetSpec) ([]*store.Delivery, error)
}

// testTag marks a delivery that came from this endpoint.
//
// Set by the server, never accepted from the caller, so a notification that
// arrived during a test is identifiable in the delivery list afterwards. "Which
// of these was the test" is the first question somebody asks when a real one
// turns up missing.
const testTag = "test"

// testNotificationRequest is the body of
// POST /admin/api/channels/{name}/test-notification.
type testNotificationRequest struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// sendTestNotification implements POST /admin/api/channels/{name}/test-notification.
//
// The operator surface deliberately has no way to send an arbitrary
// notification. It has this: a title and a body, to one named channel, through
// the ordinary delivery path — queue, worker, channel, attempt history — coming
// back with the id of the delivery it created.
//
// The gap it closes is the one that matters. "Test connectivity" answers whether
// a channel can be reached, and for five of the six channel types it cannot even
// answer that, because they have no side-effect-free probe. It never answered
// the question an operator actually has, which is whether a notification
// arrives. Before this, finding out meant leaving the interface and running
// curl, and that is the step where somebody who is not sure yet stops.
//
// What it must not become is a second way into the estate. It takes no target,
// no priority, no schedule, no links and no metadata: everything that makes the
// notification API worth authenticating is absent here. The one thing it can do
// is the thing an operator looking at the channel list already has the authority
// to do — change where this channel points and watch what happens.
func (h *handler) sendTestNotification(w http.ResponseWriter, r *http.Request) {
	t := copyFor(r)
	name := chi.URLParam(r, "name")

	if h.deps.Queue == nil {
		writeError(w, http.StatusNotImplemented, "queue_disabled", t.ErrNoQueue)
		return
	}

	cfg, err := h.deps.Channels.Get(r.Context(), name)
	if err != nil {
		h.log.Error("admin: reading a channel failed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal", t.ErrChannelUnreadable)
		return
	}
	if cfg == nil {
		writeError(w, http.StatusNotFound, "not_found", t.ErrChannelNotFound)
		return
	}
	// A disabled channel is not loaded in the router, so the delivery would sit
	// in the queue until somebody enabled it. Refusing now says why; queueing it
	// would look like the test worked.
	if !cfg.IsEnabled() {
		writeError(w, http.StatusConflict, "channel_disabled", t.ErrChannelDisabled)
		return
	}

	var req testNotificationRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", t.ErrInvalidJSON)
		return
	}

	msg := &message.Message{
		Title: strings.TrimSpace(req.Title),
		Body:  strings.TrimSpace(req.Body),
		Tags:  []string{testTag},
	}
	msg.Normalize()
	if err := msg.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_message", t.ErrTestMessageIncomplete)
		return
	}

	requestID := requestid.New()
	deliveries, err := h.deps.Queue.Enqueue(r.Context(), requestID, msg,
		[]queue.TargetSpec{{Target: name, ChannelType: cfg.Type}})
	if err != nil {
		h.log.Error("admin: enqueueing a test notification failed", slog.String("error", err.Error()))
		writeError(w, http.StatusServiceUnavailable, "queue_unavailable", t.ErrEnqueueFailed)
		return
	}

	// Audited, unlike the connectivity check. This one sends a real message to a
	// real endpoint, and "who sent that" is a question with an answer.
	h.record(r.Context(), "channel.test_notification", name,
		"queued a test notification through the delivery path")

	// The worker is nudged rather than waited for. The operator gets the id and a
	// page to watch; blocking here would make a slow channel look like a slow
	// interface, and the delivery page is the thing that can actually report
	// what happened.
	if h.deps.Waker != nil {
		h.deps.Waker.Wake()
	}

	out := map[string]any{"request_id": requestID}
	if len(deliveries) > 0 {
		out["delivery_id"] = deliveries[0].ID
	}
	writeJSON(w, http.StatusAccepted, out)
}
