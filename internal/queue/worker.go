package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"notifyrelay/internal/channel"
	"notifyrelay/internal/message"
	"notifyrelay/internal/metrics"
	"notifyrelay/internal/requestid"
	"notifyrelay/internal/router"
	"notifyrelay/internal/store"
	"notifyrelay/internal/store/spool"
)

// Deliverer is what the worker needs from the router.
type Deliverer interface {
	Deliver(ctx context.Context, requestID, target string, msg *message.Message) router.TargetResult
}

// Options configures a Worker. The zero value is not usable; use New, which
// fills in the defaults.
type Options struct {
	Store   store.Store
	Spool   *spool.Store
	Router  Deliverer
	Policy  Policy
	Log     *slog.Logger
	Metrics *metrics.Metrics

	// Workers is how many deliveries may be in flight at once.
	Workers int
	// Batch is how many deliveries a single claim takes.
	Batch int
	// PollEvery is how long an idle worker waits before looking again.
	PollEvery time.Duration
	// DeliverTimeout bounds one delivery attempt.
	DeliverTimeout time.Duration
	// ReleaseDelay is how long a released delivery waits before its next try.
	ReleaseDelay time.Duration
	// ClaimTimeout is how long a claim is good for. A delivery still in flight
	// after that is treated as abandoned and returned to the queue.
	//
	// This must exceed DeliverTimeout, or a slow delivery is mistaken for an
	// abandoned one and sent twice.
	ClaimTimeout time.Duration
	// RecoverEvery is how often the sweep runs. Defaults to a third of
	// ClaimTimeout, so an expired claim is noticed promptly rather than up to
	// a full timeout late.
	RecoverEvery time.Duration

	// PruneEvery is how often retention runs. Zero disables it.
	PruneEvery time.Duration
	// SentRetention and FailedRetention are how long terminal deliveries are
	// kept, and IdempotencyRetention how long responses are replayable.
	SentRetention        time.Duration
	FailedRetention      time.Duration
	IdempotencyRetention time.Duration
}

func (o *Options) applyDefaults() {
	if o.Workers <= 0 {
		o.Workers = 4
	}
	if o.Batch <= 0 {
		o.Batch = 16
	}
	if o.PollEvery <= 0 {
		o.PollEvery = time.Second
	}
	if o.DeliverTimeout <= 0 {
		o.DeliverTimeout = 30 * time.Second
	}
	if o.ReleaseDelay <= 0 {
		o.ReleaseDelay = 30 * time.Second
	}
	if o.ClaimTimeout <= 0 {
		// Generously longer than a delivery can take, so a slow channel is not
		// mistaken for an abandoned claim and delivered twice.
		o.ClaimTimeout = 5 * time.Minute
	}
	if o.RecoverEvery <= 0 {
		o.RecoverEvery = o.ClaimTimeout / 3
	}
	if o.SentRetention <= 0 {
		o.SentRetention = 7 * 24 * time.Hour
	}
	if o.FailedRetention <= 0 {
		o.FailedRetention = 30 * 24 * time.Hour
	}
	if o.IdempotencyRetention <= 0 {
		o.IdempotencyRetention = 24 * time.Hour
	}
	o.Policy = o.Policy.Normalize()
}

// Worker moves queued deliveries to their channels.
type Worker struct {
	opts Options

	wake chan struct{}
	once sync.Once
}

// New builds a worker.
func New(opts Options) *Worker {
	opts.applyDefaults()
	return &Worker{
		opts: opts,
		// Capacity one: a nudge that arrives while workers are busy must not
		// block the caller, and a second nudge adds nothing.
		wake: make(chan struct{}, 1),
	}
}

// Wake asks the workers to look at the queue now rather than at the next poll.
func (w *Worker) Wake() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Enqueue stores a message for each target and returns the deliveries.
//
// Bodies are written before the rows. A crash between the two leaves a spool
// file with no delivery — harmless, and the spool pruner collects it. The
// other order would leave a delivery with no body, which the worker can only
// report as an undeliverable message.
func (w *Worker) Enqueue(ctx context.Context, requestID string, msg *message.Message, targets []TargetSpec) ([]*store.Delivery, error) {
	if len(targets) == 0 {
		return nil, errors.New("queue: no targets")
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("queue: encode payload: %w", err)
	}

	now := time.Now().UTC()
	items := make([]*store.Delivery, 0, len(targets))

	for _, t := range targets {
		id := requestid.New()

		if err := w.opts.Spool.Put(id, payload); err != nil {
			return nil, fmt.Errorf("queue: spool %s: %w", id, err)
		}

		items = append(items, &store.Delivery{
			ID:            id,
			RequestID:     requestID,
			Target:        t.Target,
			ChannelType:   t.ChannelType,
			Status:        store.StatusQueued,
			NextAttemptAt: now,
			CreatedAt:     now,
			UpdatedAt:     now,
		})
	}

	if err := w.opts.Store.Enqueue(ctx, items); err != nil {
		// The bodies are on disk with nothing pointing at them; drop them
		// rather than leave the pruner to guess.
		for _, d := range items {
			w.opts.Spool.Delete(d.ID)
		}
		return nil, err
	}

	for _, d := range items {
		w.opts.Metrics.Enqueued.WithLabelValues(d.Target, d.ChannelType).Inc()
	}

	w.Wake()
	return items, nil
}

// TargetSpec is one place a message should go.
type TargetSpec struct {
	Target      string // the alias the caller used
	ChannelType string
}

// Run processes the queue until the context is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	w.recoverOrphans(ctx)
	w.refreshStats(ctx)

	if w.opts.PruneEvery > 0 {
		go w.pruneLoop(ctx)
	}

	go w.recoverLoop(ctx)

	var wg sync.WaitGroup
	for i := 0; i < w.opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.loop(ctx)
		}()
	}

	wg.Wait()
	return nil
}

func (w *Worker) loop(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.wake:
		case <-timer.C:
		}

		processed := w.drain(ctx)

		// Anything left is either not due yet or was just enqueued; either way
		// there is nothing to spin on.
		if processed == 0 {
			timer.Reset(w.opts.PollEvery)
		} else {
			timer.Reset(0)
		}
	}
}

// drain claims and delivers until nothing is due.
func (w *Worker) drain(ctx context.Context) int {
	total := 0

	for ctx.Err() == nil {
		claimed, err := w.opts.Store.Claim(ctx, w.opts.Batch, time.Now().UTC())
		if err != nil {
			w.opts.Log.Error("queue: claim failed", slog.String("error", err.Error()))
			return total
		}
		if len(claimed) == 0 {
			return total
		}

		for _, d := range claimed {
			if ctx.Err() != nil {
				// Shutting down with work in hand: put it back rather than
				// leave it in flight for the orphan recovery to find.
				w.release(ctx, d, "shutting down", time.Now().UTC())
				continue
			}
			w.process(ctx, d)
			total++
		}
	}

	return total
}

// process delivers one claimed delivery and records the outcome.
func (w *Worker) process(ctx context.Context, d *store.Delivery) {
	w.opts.Metrics.InFlight.Inc()
	defer w.opts.Metrics.InFlight.Dec()

	msg, err := w.loadMessage(d)
	if err != nil {
		// The body is gone. No retry brings it back, and pretending otherwise
		// would keep a permanently undeliverable message in the queue.
		w.deadLetter(ctx, d,
			w.attempt(d, classPayloadMissing, "spool file could not be read", err.Error(), 0),
			"payload missing")
		return
	}

	attemptCtx, cancel := context.WithTimeout(ctx, w.opts.DeliverTimeout)
	defer cancel()

	start := time.Now()
	res := w.opts.Router.Deliver(attemptCtx, d.RequestID, d.Target, msg)
	elapsed := time.Since(start)

	w.opts.Metrics.ObserveDelivery(d.ChannelType, res.Class().String(), elapsed)

	attempt := w.attempt(d, res.Class().String(), res.Detail, res.Error, elapsed.Milliseconds())

	switch res.Class() {
	case channel.ClassSent:
		if err := w.opts.Store.Complete(ctx, d.ID, attempt, time.Now().UTC()); err != nil {
			w.opts.Log.Error("queue: recording delivery failed",
				slog.String("delivery", d.ID), slog.String("error", err.Error()))
		}
		w.opts.Metrics.Delivered.WithLabelValues(d.Target, d.ChannelType).Inc()
		w.discard(d)

	case channel.ClassPermanent:
		w.deadLetter(ctx, d, attempt, res.Error)

	case channel.ClassTransient:
		// The peer gave a verdict, so this counts as an attempt.
		w.retryOrGiveUp(ctx, d, attempt, res.Error)

	default: // channel.ClassConnectError
		// Nothing reached the peer. Charging an attempt would let one
		// downstream outage burn through every message's retry budget.
		if reason := w.opts.Policy.ExhaustedReason(d.Attempts, time.Since(d.CreatedAt)); reason != "" {
			w.deadLetter(ctx, d, attempt, reason)
			return
		}
		w.release(ctx, d, "no channel capacity: "+res.Detail, time.Now().UTC().Add(w.opts.ReleaseDelay))
	}
}

// retryOrGiveUp reschedules a delivery, or dead-letters it when the policy
// says there is no point.
func (w *Worker) retryOrGiveUp(ctx context.Context, d *store.Delivery, attempt *store.Attempt, reason string) {
	age := time.Since(d.CreatedAt)

	if giveUp := w.opts.Policy.ExhaustedReason(d.Attempts+1, age); giveUp != "" {
		w.deadLetter(ctx, d, attempt, giveUp)
		return
	}

	next, ok := w.opts.Policy.Retry(d.Attempts+1, age, time.Now().UTC())
	if !ok {
		w.deadLetter(ctx, d, attempt, "attempts exhausted")
		return
	}

	if err := w.opts.Store.Reschedule(ctx, d.ID, attempt, next); err != nil {
		w.opts.Log.Error("queue: reschedule failed",
			slog.String("delivery", d.ID), slog.String("error", err.Error()))
	}
}

func (w *Worker) deadLetter(ctx context.Context, d *store.Delivery, attempt *store.Attempt, reason string) {
	if err := w.opts.Store.DeadLetter(ctx, d.ID, attempt, reason); err != nil {
		w.opts.Log.Error("queue: dead-lettering failed",
			slog.String("delivery", d.ID), slog.String("error", err.Error()))
		return
	}

	w.opts.Metrics.DeadLettered.WithLabelValues(d.Target, d.ChannelType, reason).Inc()
	w.opts.Log.Warn("queue: delivery dead-lettered",
		slog.String("delivery", d.ID),
		slog.String("request_id", d.RequestID),
		slog.String("target", d.Target),
		slog.String("reason", reason),
	)
	// The row is kept for the dead-letter list; the body is not needed to
	// answer "what failed and why", and the row may outlive its usefulness.
	w.discard(d)
}

func (w *Worker) release(ctx context.Context, d *store.Delivery, reason string, next time.Time) {
	released, err := w.opts.Store.Release(ctx, d.ID, reason, next)
	if err != nil {
		w.opts.Log.Error("queue: release failed",
			slog.String("delivery", d.ID), slog.String("error", err.Error()))
		return
	}
	if released {
		w.opts.Metrics.Released.WithLabelValues(d.Target, d.ChannelType).Inc()
	}
}

// attempt builds the audit record for a delivery outcome.
func (w *Worker) attempt(d *store.Delivery, class, detail, errMsg string, elapsedMS int64) *store.Attempt {
	return &store.Attempt{
		DeliveryID:  d.ID,
		RequestID:   d.RequestID,
		Target:      d.Target,
		ChannelType: d.ChannelType,
		AttemptNo:   d.Attempts + 1,
		Class:       class,
		Detail:      detail,
		Error:       errMsg,
		ElapsedMS:   elapsedMS,
	}
}

const classPayloadMissing = "PAYLOAD_MISSING"

func (w *Worker) loadMessage(d *store.Delivery) (*message.Message, error) {
	raw, err := w.opts.Spool.Get(d.ID)
	if err != nil {
		return nil, err
	}

	var msg message.Message
	if err := json.Unmarshal(raw, &msg); err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}
	return &msg, nil
}

// discard removes a finished delivery's body. A failure here means the pruner
// will collect it later.
func (w *Worker) discard(d *store.Delivery) {
	if err := w.opts.Spool.Delete(d.ID); err != nil {
		w.opts.Log.Warn("queue: could not remove spool file",
			slog.String("delivery", d.ID), slog.String("error", err.Error()))
	}
}

// recoverLoop returns stuck deliveries to the queue, periodically.
//
// Running this only at startup is not enough. A delivery claimed moments
// before a crash is younger than the staleness cutoff at the moment of
// restart, so the startup sweep steps over it — and if nothing looks again, it
// stays in flight forever and the notification is silently lost. That is
// exactly the message the queue exists to protect.
func (w *Worker) recoverLoop(ctx context.Context) {
	ticker := time.NewTicker(w.opts.RecoverEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.recoverOrphans(ctx)
		}
	}
}

// recoverOrphans returns deliveries whose claim has expired to the queue.
//
// The test is against the claim, not against a restart and not against
// whether anything looks wrong: every worker renews its claim by finishing
// within the timeout, so a claim older than that means nobody is coming back
// for it. That makes the sweep correct whether the previous attempt ended in a
// crash, a killed process, or a worker that simply never returned.
func (w *Worker) recoverOrphans(ctx context.Context) {
	now := time.Now().UTC()

	n, err := w.opts.Store.RecoverOrphans(ctx, w.opts.ClaimTimeout, now)
	if err != nil {
		w.opts.Log.Error("queue: orphan recovery failed", slog.String("error", err.Error()))
		return
	}
	if n > 0 {
		w.opts.Log.Warn("queue: recovered deliveries whose claim expired",
			slog.Int("count", n), slog.Duration("claim_timeout", w.opts.ClaimTimeout))
	}
}

func (w *Worker) refreshStats(ctx context.Context) {
	stats, err := w.opts.Store.Stats(ctx)
	if err != nil {
		return
	}
	w.opts.Metrics.SetQueueDepth(stats.Queued, stats.Sending, stats.Sent, stats.Failed)
}

func (w *Worker) pruneLoop(ctx context.Context) {
	ticker := time.NewTicker(w.opts.PruneEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.prune(ctx)
		}
	}
}

func (w *Worker) prune(ctx context.Context) {
	now := time.Now().UTC()

	removed, err := w.opts.Store.Prune(ctx,
		now.Add(-w.opts.SentRetention), now.Add(-w.opts.FailedRetention))
	if err != nil {
		w.opts.Log.Error("queue: prune failed", slog.String("error", err.Error()))
	}

	records, err := w.opts.Store.PruneRecords(ctx, now.Add(-w.opts.IdempotencyRetention))
	if err != nil {
		w.opts.Log.Error("queue: pruning idempotency records failed", slog.String("error", err.Error()))
	}

	// Attempts normally disappear with their delivery through ON DELETE
	// CASCADE; this catches the ones that outlived it.
	attempts, err := w.opts.Store.PruneAttempts(ctx, now.Add(-w.opts.FailedRetention))
	if err != nil {
		w.opts.Log.Error("queue: pruning attempts failed", slog.String("error", err.Error()))
	}

	// Spool files are removed by age rather than by looking up each pruned
	// delivery: a body whose row is already gone is exactly the orphan this
	// collects.
	files, err := w.opts.Spool.Prune(now.Add(-w.opts.FailedRetention))
	if err != nil {
		w.opts.Log.Error("queue: pruning spool failed", slog.String("error", err.Error()))
	}

	if removed > 0 || records > 0 || attempts > 0 || files > 0 {
		w.opts.Log.Info("queue: retention",
			slog.Int("deliveries", removed),
			slog.Int("attempts", attempts),
			slog.Int("idempotency_records", records),
			slog.Int("spool_files", files),
		)
	}

	w.refreshStats(ctx)
}
