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
	"time"

	"notifyrelay/internal/audit"
	"notifyrelay/internal/breaker"
	"notifyrelay/internal/channel"
	"notifyrelay/internal/config"
	"notifyrelay/internal/message"
	"notifyrelay/internal/quota"
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

	class channel.ResultClass
}

// Class returns the classification behind Status.
func (t TargetResult) Class() channel.ResultClass { return t.class }

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

// Router holds the configured channel instances.
type Router struct {
	instances      map[string]channel.Channel
	types          map[string]string
	quotas         map[string]quota.Limits
	deliverTimeout time.Duration
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
		instances:      make(map[string]channel.Channel),
		types:          make(map[string]string),
		quotas:         make(map[string]quota.Limits),
		deliverTimeout: opts.DeliverTimeout,
		recorder:       opts.Audit,
		limiter:        newLimiter(),
		breakers:       opts.Breaker,
		quota:          opts.Quota,
		log:            log,
	}

	for _, c := range opts.Channels {
		if !c.IsEnabled() {
			continue
		}
		if _, dup := r.instances[c.Name]; dup {
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

		r.instances[c.Name] = ch
		r.types[c.Name] = c.Type
		r.quotas[c.Name] = quota.Limits{
			PerSecond: c.Quota.PerSecond,
			PerMinute: c.Quota.PerMinute,
			PerHour:   c.Quota.PerHour,
			PerDay:    c.Quota.PerDay,
			PerMonth:  c.Quota.PerMonth,
		}
	}

	return r, nil
}

// RegisteredTypes returns the channel types this binary knows about.
func RegisteredTypes() []channel.Descriptor { return channel.Descriptors() }

// Instances returns the configured instance aliases, sorted.
func (r *Router) Instances() []string {
	out := make([]string, 0, len(r.instances))
	for name := range r.instances {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// TypeOf returns the channel type of a configured instance, or "" if unknown.
func (r *Router) TypeOf(instance string) string { return r.types[instance] }

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
		res.Status = channel.ClassConnectError.Wire()
		res.Error = "channel is not accepting deliveries"
		res.class = channel.ClassConnectError
		return r.finish(ctx, requestID, res, start)
	}

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

	var (
		overall channel.Result
		// called records whether the channel was actually invoked. A delivery
		// held back by the breaker, by a spent allowance or by a rate limit
		// says something about this deployment's budget, not about the
		// channel's health — and feeding it to the breaker would let a busy
		// hour take a perfectly good channel out of service.
		called bool
	)

	for i, part := range parts {
		// Reserve the allowance before the call, per call. A body split into
		// three parts is three calls to the endpoint, and the platform counts
		// them that way.
		reservation, ok, why := r.reserve(ctx, name)
		if !ok {
			one := channel.ConnectError(errors.New(why), why)
			if i == 0 {
				overall = one
			} else {
				overall = combine(overall, one)
			}
			break
		}

		// Rate limiting is applied per outbound message, not per request.
		if err := r.limiter.wait(ctx, name, capability.RatePerSec); err != nil {
			reservation.Release(ctx)
			one := channel.Transient(err, "gave up waiting for the channel's rate limit")
			if i == 0 {
				overall = one
			} else {
				overall = combine(overall, one)
			}
			break
		}

		sendCtx, cancel := context.WithTimeout(ctx, r.deliverTimeout)
		one := ch.Send(sendCtx, part)
		cancel()
		called = true

		// Settle by whether the peer was actually reached: a call that never
		// got there is not charged for.
		if one.Class == channel.ClassConnectError {
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

	if r.breakers != nil && called {
		r.breakers.For(name).Record(ctx, overall.Class, time.Now())
	}

	res.Status = overall.Class.Wire()
	res.Detail = overall.Detail
	res.class = overall.Class
	res.Recipients = overall.Recipients
	if overall.Err != nil {
		res.Error = overall.Err.Error()
	}

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
	return r.quota.TryReserve(ctx, name, r.quotas[name])
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

	ch, ok := r.instances[name]
	if !ok {
		return "", nil, fmt.Errorf("router: unknown channel %q (configured: %v)", name, r.Instances())
	}
	if wantType != "" && r.types[name] != wantType {
		return "", nil, fmt.Errorf("router: channel %q has type %q, not %q", name, r.types[name], wantType)
	}
	return name, ch, nil
}

func (r *Router) finish(ctx context.Context, requestID string, res TargetResult, start time.Time) TargetResult {
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
			ElapsedMS:   res.ElapsedMS,
			Recipients:  len(res.Recipients),
		})
	}
	return res
}

// combine folds a sequential part's result into the running one, keeping the
// most severe class. ResultClass is ordered Sent < ConnectError < Transient <
// Permanent, so the maximum is the outcome a caller must be told about.
func combine(a, b channel.Result) channel.Result {
	if b.Class > a.Class {
		return b
	}
	return a
}
