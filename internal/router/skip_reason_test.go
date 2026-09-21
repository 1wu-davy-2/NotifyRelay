package router

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"notifyrelay/internal/breaker"
	"notifyrelay/internal/channel"
	"notifyrelay/internal/config"
	"notifyrelay/internal/message"
)

func openBreaker(t *testing.T, r *Router, name string, failures int) {
	t.Helper()
	b := r.breakers.For(name)
	now := time.Now()
	for i := 0; i < failures; i++ {
		b.Record(context.Background(), channel.ClassTransient, now)
	}
}

// A delivery held back because the channel is known to be down reports both the
// class and the reason, and the class is NOT_ATTEMPTED rather than
// CONNECT_ERROR: nothing was connected to. The queue waits either way, but the
// audit trail no longer has to guess which of the two happened.
func TestDeliver_SkipReasonBreakerOpen(t *testing.T) {
	r, fake := gatedRouter(t, breaker.Settings{
		FailureThreshold: 2,
		SuccessThreshold: 1,
		OpenTimeout:      time.Hour,
		HalfOpenProbes:   1,
	}, config.QuotaConfig{})

	openBreaker(t, r, "gated", 2)

	res := r.Deliver(context.Background(), "req", "gated", textMessage("b"))

	if res.Class() != channel.ClassNotAttempted {
		t.Errorf("class = %v, want NOT_ATTEMPTED so the queue waits instead of spending an attempt", res.Class())
	}
	if res.Reason() != SkipBreakerOpen {
		t.Errorf("skip_reason = %q, want %q", res.Reason(), SkipBreakerOpen)
	}
	if res.Status != channel.ClassNotAttempted.Wire() {
		t.Errorf("status = %q, want %q", res.Status, channel.ClassNotAttempted.Wire())
	}

	fake.mu.Lock()
	calls := fake.calls
	fake.mu.Unlock()
	if calls != 0 {
		t.Errorf("the channel was called %d times; a skipped delivery must not reach it", calls)
	}
}

// A spent allowance is the other reason a delivery is held back, and it calls
// for a different response from whoever is on call: wait for the window, not
// chase a broken endpoint.
func TestDeliver_SkipReasonQuotaExhausted(t *testing.T) {
	r, _ := gatedRouter(t, breaker.Settings{
		FailureThreshold: 1000,
		SuccessThreshold: 1,
		OpenTimeout:      time.Hour,
		HalfOpenProbes:   1,
	}, config.QuotaConfig{PerMinute: 1})

	if res := r.Deliver(context.Background(), "req", "gated", textMessage("b")); res.Class() != channel.ClassSent {
		t.Fatalf("the first delivery: class = %v, want SENT", res.Class())
	}

	res := r.Deliver(context.Background(), "req", "gated", textMessage("b"))
	if res.Class() != channel.ClassNotAttempted {
		t.Errorf("class = %v, want NOT_ATTEMPTED", res.Class())
	}
	if res.Reason() != SkipQuotaExhausted {
		t.Errorf("skip_reason = %q, want %q", res.Reason(), SkipQuotaExhausted)
	}
}

// Giving up on a rate limit is the third.
//
// This one used to report TRANSIENT, which spent a retry attempt on a call that
// was never made. It is the same mistake the quota path avoids, and it made the
// two refusals inconsistent with each other: both mean "the channel was not
// called", so both are NOT_ATTEMPTED and neither spends an attempt.
func TestDeliver_SkipReasonRateLimited(t *testing.T) {
	r, fake := gatedRouter(t, breaker.Settings{
		FailureThreshold: 1000,
		SuccessThreshold: 1,
		OpenTimeout:      time.Hour,
		HalfOpenProbes:   1,
	}, config.QuotaConfig{})

	// One call per thousand seconds: the first is free, the second cannot be
	// waited out inside any deadline a test will accept.
	fake.ratePerSec = 0.001

	if res := r.Deliver(context.Background(), "req", "gated", textMessage("b")); res.Class() != channel.ClassSent {
		t.Fatalf("the first delivery: class = %v, want SENT", res.Class())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	res := r.Deliver(ctx, "req", "gated", textMessage("b"))
	if res.Class() != channel.ClassNotAttempted {
		t.Errorf("class = %v, want NOT_ATTEMPTED", res.Class())
	}
	if res.Reason() != SkipRateLimited {
		t.Errorf("skip_reason = %q, want %q", res.Reason(), SkipRateLimited)
	}

	fake.mu.Lock()
	calls := fake.calls
	fake.mu.Unlock()
	if calls != 1 {
		t.Errorf("the channel was called %d times, want 1 — the second call never got its slot", calls)
	}
}

// ------------------------------------------------------------------ redaction

// leakyChannel is a channel that puts a configured credential into its error,
// which is what net/http does by accident: it renders the whole URL, and for
// several real platforms the credential is inside that URL.
type leakyChannel struct{ secret string }

func (c *leakyChannel) Type() string { return leakyType }
func (c *leakyChannel) ParamSchema() []channel.ParamSpec {
	return []channel.ParamSpec{
		{Name: "token", Type: channel.ParamString, Private: true},
		{Name: "display", Type: channel.ParamString},
	}
}
func (c *leakyChannel) Capability() channel.Capability {
	return channel.Capability{SupportedFormats: []message.Format{message.FormatText}}
}
func (c *leakyChannel) Send(context.Context, *message.Message) channel.Result {
	return channel.ConnectError(
		fmt.Errorf("Post \"https://hooks.example.com/services/%s\": dial tcp: connection refused", c.secret),
		"never reached the endpoint")
}
func (c *leakyChannel) Test(context.Context) channel.Result { return channel.Sent("ok") }

const leakyType = "routertestleak"

var registerLeakyOnce sync.Once

func registerLeaky() {
	registerLeakyOnce.Do(func() {
		channel.Register(channel.Descriptor{
			Type: leakyType,
			ParamSchema: []channel.ParamSpec{
				{Name: "token", Type: channel.ParamString, Private: true},
				{Name: "display", Type: channel.ParamString},
			},
			Factory: func(_ string, cfg map[string]any) (channel.Channel, error) {
				secret, _ := cfg["token"].(string)
				return &leakyChannel{secret: secret}, nil
			},
		})
	})
}

// The contract ParamSpec.Private declares is that the value is masked on its way
// out of the process. Masking it in the /channels document and then quoting it
// in a delivery error would make the declaration a lie — and this is not
// hypothetical: the error below is what net/http produces for a Slack, DingTalk,
// Feishu or WeCom webhook, whose endpoint URL *is* the credential.
func TestDeliver_ConfiguredSecretNeverReachesTheOutcome(t *testing.T) {
	registerLeaky()

	const secret = "hunter2-super-secret-value"

	r, err := New(Options{
		Channels: []config.ChannelConfig{{
			Name: "leaky", Type: leakyType,
			Config: map[string]any{"token": secret, "display": "keep me"},
		}},
		DeliverTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	res := r.Deliver(context.Background(), "req", "leaky", textMessage("b"))

	if strings.Contains(res.Error, secret) {
		t.Errorf("the configured secret reached the outcome: %q", res.Error)
	}
	if !strings.Contains(res.Error, channel.Redacted) {
		t.Errorf("error = %q, want it to show %s where the secret was", res.Error, channel.Redacted)
	}
	// The rest of the message must survive: redaction that destroys the
	// diagnosis is its own kind of failure.
	if !strings.Contains(res.Error, "connection refused") {
		t.Errorf("error = %q, want the cause to survive redaction", res.Error)
	}
	if strings.Contains(res.Error, "keep me") {
		t.Errorf("error = %q, want non-secret configuration untouched", res.Error)
	}
}

// The invariant, from the other side.
//
// A body long enough to be split across several calls can have its first part
// delivered and its second held back. That delivery was NOT skipped: the channel
// received it. Reporting a skip reason would put "we never called it" on a
// message the channel did get, and ClassNotAttempted sorting lowest is what
// makes the fold produce the real outcome instead.
func TestDeliver_PartiallySentDeliveryIsNotSkipped(t *testing.T) {
	r, fake := gatedRouter(t, breaker.Settings{
		FailureThreshold: 1000,
		SuccessThreshold: 1,
		OpenTimeout:      time.Hour,
		HalfOpenProbes:   1,
	}, config.QuotaConfig{PerSecond: 1})

	// Split the body across several calls, so the allowance runs out partway.
	fake.bodyMaxLen = 20
	fake.overflowMode = channel.OverflowSplit

	res := r.Deliver(context.Background(), "req", "gated", textMessage(strings.Repeat("x", 200)))

	fake.mu.Lock()
	calls := fake.calls
	fake.mu.Unlock()
	if calls == 0 {
		t.Fatal("no part was sent; the test did not set up a partial send")
	}

	if res.Class() == channel.ClassNotAttempted {
		t.Errorf("class = %v, but %d part(s) reached the channel", res.Class(), calls)
	}
	if res.WasSkipped() {
		t.Errorf("skip_reason = %q on a delivery the channel received", res.Reason())
	}
	if res.Class() != channel.ClassSent {
		t.Errorf("class = %v, want the delivered part's outcome to survive the fold", res.Class())
	}
}

// The invariant stated once, for all three refusals: a skip reason is present if
// and only if the class says the channel was never called. Anything else and an
// operator reading the audit trail cannot tell a stalled deployment from a
// broken endpoint.
func TestDeliver_SkipReasonAndClassAgree(t *testing.T) {
	cases := []struct {
		name       string
		limits     config.QuotaConfig
		settings   breaker.Settings
		openIt     bool
		wantReason SkipReason
	}{
		{
			name:       "breaker open",
			settings:   breaker.Settings{FailureThreshold: 1, SuccessThreshold: 1, OpenTimeout: time.Hour, HalfOpenProbes: 1},
			openIt:     true,
			wantReason: SkipBreakerOpen,
		},
		{
			name:       "quota exhausted",
			limits:     config.QuotaConfig{PerSecond: 1},
			settings:   breaker.Settings{FailureThreshold: 1000, SuccessThreshold: 1, OpenTimeout: time.Hour, HalfOpenProbes: 1},
			wantReason: SkipQuotaExhausted,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, fake := gatedRouter(t, tc.settings, tc.limits)

			if !tc.openIt {
				// Spend the allowance on a real delivery first.
				if res := r.Deliver(context.Background(), "req", "gated", textMessage("b")); res.WasSkipped() {
					t.Fatalf("the delivery that was meant to spend the allowance was skipped: %q", res.Reason())
				}
			} else {
				openBreaker(t, r, "gated", tc.settings.FailureThreshold)
			}

			fake.mu.Lock()
			before := fake.calls
			fake.mu.Unlock()

			res := r.Deliver(context.Background(), "req", "gated", textMessage("b"))

			skipped := res.Reason() != skipNone
			notAttempted := res.Class() == channel.ClassNotAttempted
			if skipped != notAttempted {
				t.Errorf("skip_reason = %q, class = %v — the two must agree", res.Reason(), res.Class())
			}
			if res.Reason() != tc.wantReason {
				t.Errorf("skip_reason = %q, want %q", res.Reason(), tc.wantReason)
			}
			if res.Status != channel.ClassNotAttempted.Wire() {
				t.Errorf("status = %q, want %q", res.Status, channel.ClassNotAttempted.Wire())
			}

			fake.mu.Lock()
			after := fake.calls
			fake.mu.Unlock()
			if after != before {
				t.Errorf("the channel was called %d more times despite being skipped", after-before)
			}
		})
	}
}
