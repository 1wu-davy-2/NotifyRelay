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

// CapabilityOf returns what a configured instance declares about itself.
//
// It reads the live instance rather than the type's descriptor, because the
// values that matter here are per instance — whether this particular channel
// has a destination of its own depends on how it was configured, not on what
// kind of channel it is.
func (r *Router) CapabilityOf(instance string) (channel.Capability, bool) {
	ch, ok := r.current().instances[instance]
	if !ok {
		return channel.Capability{}, false
	}
	return ch.Capability(), true
}

func sortedKeys(m map[string]channel.Channel) []string {
	out := make([]string, 0, len(m))
	for name := range m {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ResolveTarget resolves a target without delivering anything.
//
// The asynchronous path needs to reject an unknown target at the moment it is
// accepted, not hours later when a worker picks the delivery up: a caller that
// gets an acknowledgement deserves one that means something.
//
// Recipients are passed in rather than attached afterwards so that the check
// cannot be half-done: resolving a target means deciding whether these
// recipients can be delivered to it, and a caller that forgot the second step
// would accept a request the channel will refuse forever.
//
// The returned Target carries the instance it resolved to. A caller that stores
// the target and delivers it later must store that field too — see
// channel.Target.Instance for why re-resolving later is a different question.
func (r *Router) ResolveTarget(ref string, recipients []string) (channel.Target, string, error) {
	target, err := r.resolveRef(ref)
	if err != nil {
		return channel.Target{}, "", err
	}

	// The request-level `to` fills in addressing for a target that does not
	// carry its own. A URL carries its own, and the two disagreeing is worth
	// saying out loud — merging them would send to addresses the caller never
	// wrote, and picking a winner would silently ignore the other.
	//
	// The check lives here rather than at the call site because only here is it
	// still known which recipients came from the reference: by the time this
	// returns they are one list.
	switch {
	case len(target.Recipients) > 0 && len(recipients) > 0:
		return channel.Target{}, "", &RecipientConflictError{Ref: ref}
	case len(target.Recipients) == 0:
		target.Recipients = recipients
	}

	ch := r.current().instances[target.Instance]
	if err := checkRecipients(target, ch.Capability()); err != nil {
		return channel.Target{}, "", err
	}
	return target, ch.Type(), nil
}

// RecipientConflictError reports a target that names its own recipients when
// the request named some as well — a `mailto:` URL and a `to` field, say.
//
// Typed for the same reason as the policy error below: the caller is told its
// request was malformed, not that its target does not exist.
type RecipientConflictError struct{ Ref string }

func (e *RecipientConflictError) Error() string {
	return fmt.Sprintf("target %q names its own recipients; do not also send a \"to\" field", e.Ref)
}

// RecipientPolicyError reports that a target cannot carry the recipients it was
// given: either the channel type takes no addressing from the caller, or the
// list is longer than it allows.
//
// Typed because the two ways a target can be refused mean different things and
// get different answers. "No channel is called that" is a fact about this
// deployment and is reported as an unknown target; "this channel has no
// recipients" is a fact about the request and is reported as a bad one. Folding
// them together would tell a caller to check its spelling when the real problem
// is that it addressed a group chat.
type RecipientPolicyError struct{ Err error }

func (e *RecipientPolicyError) Error() string { return e.Err.Error() }
func (e *RecipientPolicyError) Unwrap() error { return e.Err }

// checkRecipients enforces the channel's declared addressing policy.
//
// Both halves matter. A channel that declares no addressing must not silently
// drop a recipient list — the caller would believe a password-reset link went
// to a person when it went to a group chat. And the cap is what stops a caller
// from choosing how many connections a channel opens on one unit of its quota:
// Send is charged per call, so without a cap one request could buy fifty SMTP
// sessions for the price of one.
func checkRecipients(target channel.Target, capability channel.Capability) error {
	if len(target.Recipients) == 0 {
		return nil
	}
	if capability.MaxRecipients == 0 {
		return &RecipientPolicyError{fmt.Errorf(
			"router: target %q does not accept recipients from the request", target.Ref)}
	}
	if len(target.Recipients) > capability.MaxRecipients {
		return &RecipientPolicyError{fmt.Errorf(
			"router: target %q was given %d recipients, more than its limit of %d",
			target.Ref, len(target.Recipients), capability.MaxRecipients)}
	}
	return nil
}

// resolveRef maps a target reference to an instance and its type.
//
// Three forms are understood:
//
//	oncall                    a configured instance alias
//	email:oncall              a type-qualified alias; the type must match
//	mailto://ops@example.com  a channel URL, read by the channel itself
//
// The URL form is dispatched through the registry, so this package learns no
// scheme by name — the same rule that keeps it from naming a channel type.
func (r *Router) resolveRef(ref string) (channel.Target, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return channel.Target{}, errors.New("router: empty target")
	}

	if i := strings.Index(ref, ":"); i > 0 {
		scheme := ref[:i]
		if d, ok := channel.LookupScheme(scheme); ok {
			return r.resolveURL(ref, d)
		}
		// A scheme nobody answers is worth its own message: falling through
		// would report it as an unknown *alias* named "//ops@example.com",
		// which names the symptom and hides the cause.
		if strings.Contains(ref, "://") {
			return channel.Target{}, fmt.Errorf(
				"router: target %q uses URL scheme %q, which no registered channel answers", ref, scheme)
		}
	}

	set := r.current()

	name, wantType := ref, ""
	if t, n, ok := strings.Cut(ref, ":"); ok {
		wantType, name = strings.TrimSpace(t), strings.TrimSpace(n)
	}

	if _, ok := set.instances[name]; !ok {
		return channel.Target{}, fmt.Errorf("router: unknown channel %q (configured: %v)", name, sortedKeys(set.instances))
	}
	if wantType != "" && set.types[name] != wantType {
		return channel.Target{}, fmt.Errorf("router: channel %q has type %q, not %q", name, set.types[name], wantType)
	}
	return channel.Target{Ref: ref, Instance: name}, nil
}

// resolveURL reads a channel URL and picks the instance it borrows.
func (r *Router) resolveURL(ref string, d channel.Descriptor) (channel.Target, error) {
	target, err := d.ParseTarget(ref)
	if err != nil {
		return channel.Target{}, err
	}
	target.Ref = ref

	set := r.current()

	if target.Instance != "" {
		typ, ok := set.types[target.Instance]
		if !ok {
			return channel.Target{}, fmt.Errorf("router: target %q names channel %q, which is not configured (configured: %v)",
				ref, target.Instance, sortedKeys(set.instances))
		}
		if typ != d.Type {
			return channel.Target{}, fmt.Errorf("router: target %q needs a %s channel, but %q is a %s channel",
				ref, d.Type, target.Instance, typ)
		}
		return target, nil
	}

	// No instance named. "The only one of this type" is a fact about the
	// configuration right now, which is exactly why ResolveTarget hands the
	// winner back for the caller to record rather than leaving it to be
	// rediscovered at delivery time.
	candidates := make([]string, 0, 1)
	for name, typ := range set.types {
		if typ == d.Type {
			candidates = append(candidates, name)
		}
	}
	sort.Strings(candidates)

	switch len(candidates) {
	case 0:
		return channel.Target{}, fmt.Errorf("router: target %q needs a channel of type %q, but none is configured", ref, d.Type)
	case 1:
		target.Instance = candidates[0]
		return target, nil
	default:
		return channel.Target{}, fmt.Errorf("router: target %q does not name a channel and %d channels of type %q are configured (%s); add ?via=<name>",
			ref, len(candidates), d.Type, strings.Join(candidates, ", "))
	}
}

// PermanentFailure is the outcome for a target the caller never got as far as
// delivering to, such as one whose reference does not resolve.
//
// Exported so a caller can report a resolution failure as one entry among the
// results rather than as a status code — which is what the synchronous path
// promises: one bad target must not hide the fate of the others.
func PermanentFailure(ref string, err error) TargetResult {
	return TargetResult{
		Target: ref,
		Status: channel.ClassPermanent.Wire(),
		Error:  err.Error(),
		class:  channel.ClassPermanent,
	}
}

// DeliverAll fans msg out to every target concurrently, preserving input order
// in the returned slice.
//
// One slow target must not delay the others, and a failure on one target must
// never mask the outcome of the rest — the caller reports each independently.
func (r *Router) DeliverAll(ctx context.Context, requestID string, targets []channel.Target, msg *message.Message) []TargetResult {
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
func (r *Router) Deliver(ctx context.Context, requestID string, target channel.Target, msg *message.Message) TargetResult {
	start := time.Now()

	name, ch, err := r.pinTarget(target)
	if err != nil {
		return r.finish(ctx, requestID, TargetResult{
			Target: target.Ref,
			Status: channel.ClassPermanent.Wire(),
			Error:  err.Error(),
			class:  channel.ClassPermanent,
		}, start)
	}
	delivery := channel.Target{Ref: target.Ref, Instance: name, Recipients: target.Recipients}

	res := TargetResult{
		Target:      target.Ref,
		Channel:     name,
		ChannelType: ch.Type(),
	}

	// Checked here as well as at accept time, because not every caller comes
	// through the accept path: a delivery read back from the queue carries
	// recipients that were validated against the configuration as it stood
	// then, and the channel's own capability may have changed since.
	if err := checkRecipients(delivery, ch.Capability()); err != nil {
		res.Status = channel.ClassPermanent.Wire()
		res.Error = err.Error()
		res.class = channel.ClassPermanent
		return r.finish(ctx, requestID, res, start)
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
		one := ch.Send(sendCtx, part, delivery)
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

// pinTarget completes a delivery's target and returns the channel to use.
//
// An instance pinned at accept time is honoured as it stands; only a target
// that disagrees with it is refused. Re-resolving a pinned target instead would
// defeat the pinning: a URL that named no instance was resolved to "the only
// email channel", and if a second one has been configured since, re-resolving
// turns an already-acknowledged delivery into an ambiguous one that can only
// fail. The delivery was accepted against a configuration, and it is delivered
// against that configuration.
func (r *Router) pinTarget(t channel.Target) (string, channel.Channel, error) {
	set := r.current()

	if t.Instance == "" {
		resolved, err := r.resolveRef(t.Ref)
		if err != nil {
			return "", nil, err
		}
		t = resolved
	}

	ch, ok := set.instances[t.Instance]
	if !ok {
		return "", nil, fmt.Errorf(
			"router: channel %q is not configured (it may have been deleted since this delivery was accepted)",
			t.Instance)
	}

	// The pinned instance still has to be the right *kind* for the ref. Both
	// fields come out of the same resolution, so a disagreement means they were
	// assembled by something else — a hand-edited row, say — and pointing a
	// mailto: target at a group chat would drop the recipients on the floor.
	if i := strings.Index(t.Ref, ":"); i > 0 {
		if d, ok := channel.LookupScheme(t.Ref[:i]); ok && d.Type != set.types[t.Instance] {
			return "", nil, fmt.Errorf("router: target %q needs a %s channel, but %q is a %s channel",
				t.Ref, d.Type, t.Instance, set.types[t.Instance])
		}
	}

	return t.Instance, ch, nil
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
