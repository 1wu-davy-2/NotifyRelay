// Package router fans a message out to channel instances.
//
// This is the "adding a channel must not touch the core" boundary: nothing in
// this package names a concrete channel. It resolves a target through the
// registry, adapts the message to the channel's declared capabilities, and
// records the outcome. Rate limiting joins the same path in M2.
package router

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"notifyrelay/internal/audit"
	"notifyrelay/internal/breaker"
	"notifyrelay/internal/channel"
	"notifyrelay/internal/config"
	"notifyrelay/internal/message"
	"notifyrelay/internal/quota"
)

// SkipReason names why a delivery never reached its channel.
//
// It exists because the class alone cannot say which limit was hit. All three
// refusals report ClassNotAttempted — that is what the class is for — but an
// operator staring at a stalled queue needs to know whether to chase the
// endpoint or wait for a window to roll over, and those are opposite actions.
//
// INVARIANT: SkipReason is set if and only if the class is
// channel.ClassNotAttempted. The two are assigned together below and neither is
// meaningful without the other. A delivery whose first part went out and whose
// second was held back reports no reason: the channel did receive it, so there
// is no limit to name, and folding the un-attempted part into the class yields
// the real outcome because ClassNotAttempted sorts lowest.
type SkipReason string

const (
	// SkipBreakerOpen: the channel is known to be failing.
	SkipBreakerOpen SkipReason = "breaker_open"
	// SkipQuotaExhausted: the channel's allowance for the window is spent.
	SkipQuotaExhausted SkipReason = "quota_exhausted"
	// SkipRateLimited: waiting for the channel's rate limit would outlast the
	// delivery's own deadline.
	SkipRateLimited SkipReason = "rate_limited"

	// skipNone is what an attempted delivery reports.
	skipNone SkipReason = ""
)

// TargetResult is the outcome for one target of a request.
type TargetResult struct {
	Target      string              `json:"target"`
	Channel     string              `json:"channel,omitempty"`
	ChannelType string              `json:"channel_type,omitempty"`
	Status      string              `json:"status"`
	Error       string              `json:"error,omitempty"`
	Detail      string              `json:"detail,omitempty"`
	ElapsedMS   int64               `json:"elapsed_ms"`
	Recipients  []channel.Recipient `json:"recipients,omitempty"`

	// SkipReason is present only when the channel was never called.
	SkipReason SkipReason `json:"skip_reason,omitempty"`

	class channel.ResultClass
}

// Class returns the classification behind Status.
func (t TargetResult) Class() channel.ResultClass { return t.class }

// Reason returns why the channel was skipped, or "" if it was called.
func (t TargetResult) Reason() SkipReason { return t.SkipReason }

// WasSkipped reports whether the channel was never invoked.
func (t TargetResult) WasSkipped() bool { return t.SkipReason != skipNone }

// Options configures a Router.
type Options struct {
	// Channels are the channel instances to build.
	Channels []config.ChannelConfig
	// DeliverTimeout bounds one call to a channel.
	DeliverTimeout time.Duration
	// Audit records delivery outcomes.
	Audit audit.Recorder
	// Breaker suspends channels that are failing. Optional.
	Breaker *breaker.Manager
	// Quota reserves a channel's send allowance before it is called. Optional.
	Quota *quota.Limiter
	// Log is used for operational messages.
	Log *slog.Logger
}

// instanceSet is everything the router derives from the configured channels.
//
// It is one immutable value behind a pointer rather than four maps behind a
// lock, because it is replaced wholesale by Reload: a reader takes one atomic
// load and then works from a consistent snapshot, and a reload cannot be
// observed half-applied — seeing the new channel's type before its instance
// exists would be a delivery that resolves to a nil channel.
//
// Immutable after construction. Reload builds a new one; nothing mutates a set
// that readers may be holding.
type instanceSet struct {
	instances map[string]channel.Channel
	types     map[string]string
	quotas    map[string]quota.Limits
	secrets   map[string][]string
}

// Router holds the configured channel instances.
type Router struct {
	set atomic.Pointer[instanceSet]

	// deliverTimeout is swapped by SetDeliverTimeout: an operator raising a
	// timeout during an incident should not have to restart the service that
	// is holding the queue they are trying to drain.
	deliverTimeout atomic.Int64
	recorder       audit.Recorder
	limiter        *limiter
	breakers       *breaker.Manager
	quota          *quota.Limiter
	log            *slog.Logger
}

// New builds every enabled channel instance declared in the configuration.
//
// Disabled instances are not constructed at all, so a disabled channel with a
// bad configuration cannot prevent startup.
func New(opts Options) (*Router, error) {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}

	r := &Router{
		recorder: opts.Audit,
		limiter:        newLimiter(),
		breakers:       opts.Breaker,
		quota:          opts.Quota,
		log:            log,
	}

	set, err := buildSet(opts.Channels)
	if err != nil {
		return nil, err
	}
	r.set.Store(set)
	r.SetDeliverTimeout(opts.DeliverTimeout)

	return r, nil
}

// SetDeliverTimeout replaces the per-call deadline.
//
// A delivery already in flight keeps the deadline it started with; the new one
// applies to calls made after it. Changing the timeout under a call in progress
// would either cut it short for no reason or extend a deadline the caller has
// already accounted for.
func (r *Router) SetDeliverTimeout(d time.Duration) {
	r.deliverTimeout.Store(int64(d))
}

// DeliverTimeout returns the live per-call deadline.
func (r *Router) DeliverTimeout() time.Duration {
	return time.Duration(r.deliverTimeout.Load())
}

// buildSet constructs an instance set from configuration.
//
// Every channel is built and validated before anything is published, so a
// configuration with one bad channel produces no set at all rather than a
// partial one. That matters more for Reload than for New: a reload that half
// succeeded would leave the router serving a mixture of the old configuration
// and the new one, and the operator would have no way to tell which channel
// took effect.
func buildSet(channels []config.ChannelConfig) (*instanceSet, error) {
	set := &instanceSet{
		instances: make(map[string]channel.Channel),
		types:     make(map[string]string),
		quotas:    make(map[string]quota.Limits),
		secrets:   make(map[string][]string),
	}

	for _, c := range channels {
		if !c.IsEnabled() {
			continue
		}
		if _, dup := set.instances[c.Name]; dup {
			return nil, fmt.Errorf("router: duplicate channel instance %q", c.Name)
		}

		ch, err := channel.New(c.Type, c.Name, c.Config)
		if err != nil {
			return nil, err
		}

		// The constructor reads the parameters it knows about and ignores the
		// rest, so a typo'd key would otherwise pass unnoticed until somebody
		// noticed the wrong endpoint receiving notifications. The schema knows
		// the full set; check against it.
		if err := channel.ValidateParams(c.Type, ch.ParamSchema(), c.Config); err != nil {
			return nil, fmt.Errorf("channel %q (type %q): %w", c.Name, c.Type, err)
		}

		set.instances[c.Name] = ch
		set.types[c.Name] = c.Type
		set.quotas[c.Name] = quota.Limits{
			PerSecond: c.Quota.PerSecond,
			PerMinute: c.Quota.PerMinute,
			PerHour:   c.Quota.PerHour,
			PerDay:    c.Quota.PerDay,
			PerMonth:  c.Quota.PerMonth,
		}
		// Read once, here, from the schema the channel already declares. Doing
		// it per delivery would re-walk the same handful of parameters on the
		// hot path, and a credential's value does not change while the process
		// runs.
		set.secrets[c.Name] = channel.SecretValues(ch.ParamSchema(), c.Config)
	}

	return set, nil
}

// current returns the live instance set.
func (r *Router) current() *instanceSet { return r.set.Load() }

// Reload rebuilds the channel instances from configuration.
//
// A delivery already in flight keeps the channel object it resolved — it holds
// a reference, and the old set stays alive until nothing points at it. What
// changes is what the *next* delivery resolves. That is what makes "add a
// channel without a restart" safe rather than merely convenient: there is no
// moment at which a delivery in progress is looking at a channel that has been
// taken away from it.
//
// A configuration that fails to build leaves the running set untouched and
// returns the error. The caller decides whether to surface it; the service
// keeps delivering on the configuration that works.
func (r *Router) Reload(channels []config.ChannelConfig) error {
	set, err := buildSet(channels)
	if err != nil {
		return err
	}

	before := r.current()
	r.set.Store(set)

	r.log.Info("router: channel configuration reloaded",
		slog.Any("before", sortedKeys(before.instances)),
		slog.Any("after", sortedKeys(set.instances)),
	)
	return nil
}

// RegisteredTypes returns the channel types this binary knows about.
func RegisteredTypes() []channel.Descriptor { return channel.Descriptors() }

// Instances returns the configured instance aliases, sorted.
func (r *Router) Instances() []string { return sortedKeys(r.current().instances) }

// TypeOf returns the channel type of a configured instance, or "" if unknown.
func (r *Router) TypeOf(instance string) string { return r.current().types[instance] }

func sortedKeys(m map[string]channel.Channel) []string {
	out := make([]string, 0, len(m))
	for name := range m {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// TargetType resolves a target without delivering anything.
//
// The asynchronous path needs to reject an unknown target at the moment it is
// accepted, not hours later when a worker picks the delivery up: a caller that
// gets an acknowledgement deserves one that means something.
func (r *Router) TargetType(target string) (alias, channelType string, err error) {
	name, ch, err := r.resolve(target)
	if err != nil {
		return "", "", err
	}
	return name, ch.Type(), nil
}

// DeliverAll fans msg out to every target concurrently, preserving input order
// in the returned slice.
//
// One slow target must not delay the others, and a failure on one target must
// never mask the outcome of the rest — the caller reports each independently.
func (r *Router) DeliverAll(ctx context.Context, requestID string, targets []string, msg *message.Message) []TargetResult {
	results := make([]TargetResult, len(targets))

	var wg sync.WaitGroup
	for i, target := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = r.Deliver(ctx, requestID, target, msg)
		}()
	}
	wg.Wait()

	return results
}

// Deliver sends msg to a single target.
func (r *Router) Deliver(ctx context.Context, requestID, target string, msg *message.Message) TargetResult {
	start := time.Now()

	name, ch, err := r.resolve(target)
	if err != nil {
		return r.finish(ctx, requestID, TargetResult{
			Target: target,
			Status: channel.ClassPermanent.Wire(),
			Error:  err.Error(),
			class:  channel.ClassPermanent,
		}, start)
	}

	res := TargetResult{
		Target:      target,
		Channel:     name,
		ChannelType: ch.Type(),
	}

	// A channel that is known to be failing is skipped before anything is
	// committed to it. This is the no-capacity path, and it comes first on
	// purpose: a message held back because the channel is down must not also
	// spend the channel's allowance, or an outage would eat the day's quota
	// without a single message being sent.
	if r.breakers != nil && !r.allowDelivery(ctx, name) {
		const why = "channel is not accepting deliveries"
		res.Status = channel.ClassNotAttempted.Wire()
		res.Error = why
		res.SkipReason = SkipBreakerOpen
		res.class = channel.ClassNotAttempted
		return r.finish(ctx, requestID, res, start)
	}

	// called records whether the channel was actually invoked. A delivery held
	// back by the breaker, by a spent allowance or by a rate limit says
	// something about this deployment's budget, not about the channel's health
	// — and feeding it to the breaker would let a busy hour take a perfectly
	// good channel out of service.
	//
	// skipped names which of those it was. Both are read by the deferred
	// settlement below, so they are declared before it.
	var (
		overall channel.Result
		called  bool
		skipped SkipReason
	)

	// Admitting a delivery may have taken a half-open probe slot, and every
	// path out of this function has to give it back — by reporting an outcome,
	// or by abandoning it when the channel was never called. Deferred rather
	// than repeated at each return on purpose: the path that forgot would
	// strand the slot for the life of the process, and with half_open_probes: 1
	// a single stranded slot is a channel that refuses every delivery until a
	// restart. That is a worse outage than the one the breaker was protecting
	// against.
	defer func() {
		if r.breakers == nil {
			return
		}
		b := r.breakers.For(name)
		now := time.Now()
		if called {
			b.Record(ctx, overall.Class, now)
			return
		}
		b.Abandon(ctx, now)
	}()

	// The channel declares what it can render and how long it may be; the core
	// does the adapting. Channel implementations never convert formats.
	capability := ch.Capability()
	adapted := Adapt(msg, capability)
	parts, err := Apply(adapted, capability)
	if err != nil {
		res.Status = channel.ClassPermanent.Wire()
		res.Error = err.Error()
		res.class = channel.ClassPermanent
		return r.finish(ctx, requestID, res, start)
	}

	for i, part := range parts {
		// Reserve the allowance before the call, per call. A body split into
		// three parts is three calls to the endpoint, and the platform counts
		// them that way.
		reservation, ok, why := r.reserve(ctx, name)
		if !ok {
			skipped = SkipQuotaExhausted
			one := channel.NotAttempted(errors.New(why), why)
			if i == 0 {
				overall = one
			} else {
				overall = combine(overall, one)
			}
			break
		}

		// Rate limiting is applied per outbound message, not per request.
		//
		// Giving up here is ClassNotAttempted, not ClassTransient. It used to
		// be Transient, which spent a retry attempt on a call that was never
		// made — the same mistake the quota path avoids. Waiting for a token
		// and running out of time says nothing about the channel.
		if err := r.limiter.wait(ctx, name, capability.RatePerSec); err != nil {
			reservation.Release(ctx)
			skipped = SkipRateLimited
			one := channel.NotAttempted(err, "gave up waiting for the channel's rate limit")
			if i == 0 {
				overall = one
			} else {
				overall = combine(overall, one)
			}
			break
		}

		sendCtx, cancel := context.WithTimeout(ctx, r.DeliverTimeout())
		one := ch.Send(sendCtx, part)
		cancel()
		called = true

		if one.Class == channel.ClassNotAttempted {
			// A channel cannot report "not attempted": it just ran, so it is
			// answering. Something did try to reach the peer and did not get
			// there, which is exactly ClassConnectError — and normalising here
			// is what keeps the skip_reason invariant true for every possible
			// channel, not just the well-behaved ones.
			one.Class = channel.ClassConnectError
		}

		// Settle by whether the peer was actually reached: a call that never
		// got there — or was never made — is not charged for.
		if one.Class == channel.ClassConnectError || one.Class == channel.ClassNotAttempted {
			reservation.Release(ctx)
		} else {
			reservation.Commit(ctx)
		}

		if i == 0 {
			overall = one
		} else {
			overall = combine(overall, one)
		}
	}

	res.Detail = overall.Detail
	res.Recipients = overall.Recipients
	if overall.Err != nil {
		res.Error = overall.Err.Error()
	}

	// Only a delivery that never reached the channel reports a reason, and the
	// reason and the class are written together here so the invariant between
	// them cannot drift apart.
	//
	// The class is forced rather than read from `overall` even though the two
	// agree on every path that exists today — every refusal assigns
	// ClassNotAttempted and breaks. Forcing it means a future path that reaches
	// here without calling the channel still cannot report a class it did not
	// earn. The direction of the mistake matters: an over-eager
	// "not attempted" makes the queue wait, while an unearned "sent" loses a
	// notification.
	//
	// A delivery whose first part went out and whose second was held back is not
	// skipped. The channel did receive it, and the held-back part folded into
	// `overall` as the lowest-severity class precisely so that it disappears.
	if !called {
		res.SkipReason = skipped
		res.class = channel.ClassNotAttempted
	} else {
		res.class = overall.Class
	}
	res.Status = res.class.Wire()

	return r.finish(ctx, requestID, res, start)
}

// allowDelivery consults the channel's breaker.
func (r *Router) allowDelivery(ctx context.Context, name string) bool {
	b := r.breakers.For(name)

	ok, why := b.Allow(ctx, time.Now())
	if !ok {
		r.log.Debug("router: channel suspended",
			slog.String("channel", name), slog.String("reason", why))
	}
	return ok
}

// reserve claims one unit of the channel's allowance.
func (r *Router) reserve(ctx context.Context, name string) (*quota.Reservation, bool, string) {
	if r.quota == nil {
		return nil, true, ""
	}
	return r.quota.TryReserve(ctx, name, r.current().quotas[name])
}

// resolve maps a target string to a configured channel instance.
//
// Supported forms:
//
//	oncall        configured instance alias
//	email:oncall  type-qualified alias; the type must match the instance's type
//
// The URL form (mailto://..., dingtalk://...) arrives in M2 together with the
// registry-driven URL parser; it is rejected explicitly rather than silently
// treated as an alias.
func (r *Router) resolve(target string) (string, channel.Channel, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", nil, errors.New("router: empty target")
	}
	if strings.Contains(target, "://") {
		return "", nil, fmt.Errorf(
			"router: URL targets are not supported yet (got %q); use a configured channel alias", target)
	}

	name, wantType := target, ""
	if t, n, ok := strings.Cut(target, ":"); ok {
		wantType, name = strings.TrimSpace(t), strings.TrimSpace(n)
	}

	set := r.current()
	ch, ok := set.instances[name]
	if !ok {
		return "", nil, fmt.Errorf("router: unknown channel %q (configured: %v)", name, sortedKeys(set.instances))
	}
	if wantType != "" && set.types[name] != wantType {
		return "", nil, fmt.Errorf("router: channel %q has type %q, not %q", name, set.types[name], wantType)
	}
	return name, ch, nil
}

// finish is the single exit from Deliver, which makes it the one place that has
// to be right about what leaves the process.
func (r *Router) finish(ctx context.Context, requestID string, res TargetResult, start time.Time) TargetResult {
	// A configured credential must never appear in an outcome. The httpx fix
	// stops a transport error from carrying the endpoint URL, which is where
	// several channels keep their token; this covers everything else — a
	// response body echoed into a detail line, or a channel implementation that
	// put a password into its own error without thinking about it.
	secrets := r.current().secrets[res.Channel]
	res.Error = channel.Redact(res.Error, secrets)
	res.Detail = channel.Redact(res.Detail, secrets)

	res.ElapsedMS = time.Since(start).Milliseconds()
	if r.recorder != nil {
		r.recorder.Record(ctx, audit.Entry{
			RequestID:   requestID,
			Target:      res.Target,
			Channel:     res.Channel,
			ChannelType: res.ChannelType,
			Class:       res.class,
			Detail:      res.Detail,
			Err:         res.Error,
			SkipReason:  string(res.SkipReason),
			ElapsedMS:   res.ElapsedMS,
			Recipients:  len(res.Recipients),
		})
	}
	return res
}

// combine folds a sequential part's result into the running one, keeping the
// most severe class.
//
// ResultClass is ordered by severity —
// NotAttempted < Sent < ConnectError < Transient < Permanent — so the maximum
// is the outcome a caller must be told about. ClassNotAttempted sorting lowest
// is what makes that work: a part that was held back after an earlier part was
// delivered must not turn the whole delivery into "never attempted".
func combine(a, b channel.Result) channel.Result {
	if b.Class > a.Class {
		return b
	}
	return a
}
