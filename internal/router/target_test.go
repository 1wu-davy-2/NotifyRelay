package router

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"notifyrelay/internal/channel"
	"notifyrelay/internal/config"
	"notifyrelay/internal/message"
)

// ------------------------------------------------- a channel with a URL form

const (
	urlFakeType   = "routertesturl"
	urlFakeScheme = "routertesturl"
	// Small, so a test can exceed it without a fifty-line fixture.
	urlFakeMaxRecipients = 3
)

var urlFakeOnce sync.Once

// registerURLFake registers a channel type that takes addressing from the
// request, which is what the URL form and the `to` field both need. The real
// one is email; using a double here is what shows the router needs no knowledge
// of *which* channel it is routing.
func registerURLFake() {
	urlFakeOnce.Do(func() {
		channel.Register(channel.Descriptor{
			Type: urlFakeType,
			Capability: channel.Capability{
				SupportedFormats: []message.Format{message.FormatText},
				MaxRecipients:    urlFakeMaxRecipients,
			},
			Factory: func(_ string, _ map[string]any) (channel.Channel, error) {
				return &fakeChannel{typeName: urlFakeType, maxRecipients: urlFakeMaxRecipients}, nil
			},
			TargetScheme: urlFakeScheme,
			// Deliberately idiosyncratic: the path carries the addresses and
			// the query names the instance, but nothing in the router assumes
			// either — if it did, this function could not be this short.
			ParseTarget: func(raw string) (channel.Target, error) {
				t := channel.Target{Ref: raw}
				rest, ok := strings.CutPrefix(raw, urlFakeScheme+":")
				if !ok {
					return t, errTestScheme
				}
				path, query, _ := strings.Cut(rest, "?")
				path = strings.TrimPrefix(path, "//")
				for _, addr := range strings.Split(path, ",") {
					if addr = strings.TrimSpace(addr); addr != "" {
						t.Recipients = append(t.Recipients, addr)
					}
				}
				if v, ok := strings.CutPrefix(query, "via="); ok {
					t.Instance = v
				}
				return t, nil
			},
		})
	})
}

var errTestScheme = &testError{"target is not a " + urlFakeScheme + ": target"}

type testError struct{ s string }

func (e *testError) Error() string { return e.s }

func newURLTestRouter(t *testing.T, cfgs ...config.ChannelConfig) *Router {
	t.Helper()
	registerURLFake()
	return newTestRouter(t, cfgs...)
}

// -------------------------------------------------------------- the URL form

func TestResolveTarget_URLBorrowsTheNamedInstance(t *testing.T) {
	r := newURLTestRouter(t,
		config.ChannelConfig{Name: "known", Type: urlFakeType},
		config.ChannelConfig{Name: "other", Type: urlFakeType},
	)

	got, typ, err := r.ResolveTarget("routertesturl://a@x.test?via=other", nil)
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if typ != urlFakeType {
		t.Errorf("type = %q, want %q", typ, urlFakeType)
	}
	if got.Instance != "other" {
		t.Errorf("instance = %q, want \"other\" — via must choose, not the first match", got.Instance)
	}
	if len(got.Recipients) != 1 || got.Recipients[0] != "a@x.test" {
		t.Errorf("recipients = %v, want [a@x.test]", got.Recipients)
	}
	if got.Ref != "routertesturl://a@x.test?via=other" {
		t.Errorf("Ref = %q, want the caller's own string", got.Ref)
	}
}

func TestResolveTarget_URLFallsBackToTheOnlyInstance(t *testing.T) {
	r := newURLTestRouter(t, config.ChannelConfig{Name: "only", Type: urlFakeType})

	got, _, err := r.ResolveTarget("routertesturl://a@x.test", nil)
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if got.Instance != "only" {
		t.Errorf("instance = %q, want \"only\"", got.Instance)
	}
}

// The fallback is a statement about the configuration at this moment, which is
// why the answer is carried back for the caller to record rather than left to
// be worked out again later.
func TestResolveTarget_URLWithoutViaIsAmbiguousWhenTwoExist(t *testing.T) {
	r := newURLTestRouter(t,
		config.ChannelConfig{Name: "alpha", Type: urlFakeType},
		config.ChannelConfig{Name: "beta", Type: urlFakeType},
	)

	_, _, err := r.ResolveTarget("routertesturl://a@x.test", nil)
	if err == nil {
		t.Fatal("expected an ambiguity error")
	}
	// Both candidates are named, so the caller can pick one without going to
	// read the configuration.
	for _, want := range []string{"alpha", "beta", "via"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

func TestResolveTarget_URLWithNoInstanceOfThatType(t *testing.T) {
	r := newTestRouter(t, config.ChannelConfig{Name: "known", Type: "routertest"})

	_, _, err := r.ResolveTarget("routertesturl://a@x.test", nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), urlFakeType) {
		t.Errorf("error %q should name the type it needs", err)
	}
}

func TestResolveTarget_ViaMustBeOfTheSchemeSType(t *testing.T) {
	r := newURLTestRouter(t,
		config.ChannelConfig{Name: "known", Type: "routertest"},
	)

	_, _, err := r.ResolveTarget("routertesturl://a@x.test?via=known", nil)
	if err == nil {
		t.Fatal("expected a type mismatch error")
	}
	if !strings.Contains(err.Error(), "known") {
		t.Errorf("error %q should name the instance", err)
	}
}

// A delivery that was accepted with its instance pinned is delivered to that
// instance even after the configuration would no longer resolve the same way.
// The alternative is a message that was acknowledged at 202 and then dead-
// letters because somebody added a second channel in between.
func TestDeliver_PinnedInstanceSurvivesAnAmbiguousConfiguration(t *testing.T) {
	r := newURLTestRouter(t, config.ChannelConfig{Name: "only", Type: urlFakeType})

	target, _, err := r.ResolveTarget("routertesturl://a@x.test", nil)
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}

	// The second instance arrives after the delivery was accepted.
	if err := r.Reload([]config.ChannelConfig{
		{Name: "only", Type: urlFakeType},
		{Name: "second", Type: urlFakeType},
	}); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	res := r.Deliver(context.Background(), "req", target, textMessage("b"))
	if res.Class() != channel.ClassSent {
		t.Fatalf("class = %v (%v), want SENT", res.Class(), res.Error)
	}
	if res.Channel != "only" {
		t.Errorf("delivered through %q, want the pinned \"only\"", res.Channel)
	}
}

// A pinned instance is still checked against the reference's scheme, so a
// hand-assembled pair cannot send a mailto: target through a group chat.
func TestDeliver_PinnedInstanceMustMatchTheSchemeSType(t *testing.T) {
	r := newURLTestRouter(t,
		config.ChannelConfig{Name: "chat", Type: "routertest"},
	)

	res := r.Deliver(context.Background(), "req", channel.Target{
		Ref:      "routertesturl://a@x.test",
		Instance: "chat",
	}, textMessage("b"))
	if res.Class() != channel.ClassPermanent {
		t.Errorf("class = %v, want PERMANENT", res.Class())
	}
}

// ---------------------------------------------------------- recipient policy

func TestDeliver_RecipientsOnAChannelThatTakesNone(t *testing.T) {
	registerFake()
	r := newTestRouter(t, config.ChannelConfig{Name: "chat", Type: "routertest"})

	res := r.Deliver(context.Background(), "req", channel.Target{
		Ref:        "chat",
		Recipients: []string{"user@example.com"},
	}, textMessage("b"))

	if res.Class() != channel.ClassPermanent {
		t.Fatalf("class = %v, want PERMANENT", res.Class())
	}
	if !strings.Contains(res.Error, "does not accept recipients") {
		t.Errorf("error %q should say why", res.Error)
	}

	// Silently dropping the list would be worse than refusing it: the caller
	// would believe a password-reset link reached a person.
	if got := fakeCalls(t, r, "chat"); got != 0 {
		t.Errorf("the channel was called %d times; it must not be called at all", got)
	}
}

func TestDeliver_RecipientsOverTheCap(t *testing.T) {
	r := newURLTestRouter(t, config.ChannelConfig{Name: "tx", Type: urlFakeType})

	tooMany := make([]string, urlFakeMaxRecipients+1)
	for i := range tooMany {
		tooMany[i] = "a@x.test"
	}

	_, _, err := r.ResolveTarget("tx", tooMany)
	if err == nil {
		t.Fatal("expected the cap to be enforced at accept time")
	}

	var policy *RecipientPolicyError
	if !errors.As(err, &policy) {
		t.Fatalf("error %v should be a RecipientPolicyError, so a caller can tell it "+
			"from an unknown target", err)
	}
	if !strings.Contains(err.Error(), "limit") {
		t.Errorf("error %q should mention the limit", err)
	}
}

// The recipients reach the channel, which is the point of carrying them through
// the whole path.
func TestDeliver_PassesRecipientsToTheChannel(t *testing.T) {
	r := newURLTestRouter(t, config.ChannelConfig{Name: "tx", Type: urlFakeType})

	res := r.Deliver(context.Background(), "req", channel.Target{
		Ref:        "tx",
		Recipients: []string{"a@x.test", "b@x.test"},
	}, textMessage("b"))
	if res.Class() != channel.ClassSent {
		t.Fatalf("class = %v (%v), want SENT", res.Class(), res.Error)
	}

	ch := r.current().instances["tx"].(*fakeChannel)
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if len(ch.lastTarget.Recipients) != 2 {
		t.Errorf("channel saw %v, want both recipients", ch.lastTarget.Recipients)
	}
	if ch.lastTarget.Instance != "tx" {
		t.Errorf("channel saw instance %q, want \"tx\"", ch.lastTarget.Instance)
	}
}

func TestResolveTarget_UnknownAliasStillFails(t *testing.T) {
	r := newTestRouter(t, config.ChannelConfig{Name: "known", Type: "routertest"})

	if _, _, err := r.ResolveTarget("nope", nil); err == nil {
		t.Error("an unknown alias must not resolve")
	}
}

// fakeCalls reports how many times a channel instance was called.
func fakeCalls(t *testing.T, r *Router, name string) int {
	t.Helper()
	ch, ok := r.current().instances[name].(*fakeChannel)
	if !ok {
		t.Fatalf("instance %q is not the fake channel", name)
	}
	ch.mu.Lock()
	defer ch.mu.Unlock()
	return ch.calls
}
