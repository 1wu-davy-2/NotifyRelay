package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"notifyrelay/internal/auth"
	"notifyrelay/internal/channel"
	"notifyrelay/internal/config"
	"notifyrelay/internal/message"
	"notifyrelay/internal/queue"
	"notifyrelay/internal/router"
	"notifyrelay/internal/store"
)

// A second fake, this one declaring that it takes recipients from the request.
// The plain `apitest` fake declares none, which is what the "does not accept
// recipients" cases need.
const addrFakeType = "apitestaddr"

var registerAddrOnce sync.Once

func registerAddrChannel() {
	registerAddrOnce.Do(func() {
		channel.Register(channel.Descriptor{
			Type:         addrFakeType,
			Capability:   channel.Capability{SupportedFormats: []message.Format{message.FormatText}, MaxRecipients: 3},
			TargetScheme: addrFakeType,
			Factory: func(instance string, _ map[string]any) (channel.Channel, error) {
				return &fakeChannel{instance: instance, typeName: addrFakeType, maxRecipients: 3}, nil
			},
			ParseTarget: func(raw string) (channel.Target, error) {
				t := channel.Target{Ref: raw}
				rest, _ := strings.CutPrefix(raw, addrFakeType+":")
				// One address per target, in a path-shaped URL. What matters
				// here is not the syntax but that the router asks the channel
				// to read it.
				rest = strings.TrimPrefix(rest, "//")
				addr, query, _ := strings.Cut(rest, "?")
				if addr != "" {
					t.Recipients = []string{addr}
				}
				if v, ok := strings.CutPrefix(query, "via="); ok {
					t.Instance = v
				}
				return t, nil
			},
		})
	})
}

// newAddrHandler builds a handler whose single API key may address the given
// recipients. An empty list is the default and means "none".
func newAddrHandler(t *testing.T, allowed []string, channels ...config.ChannelConfig) http.Handler {
	t.Helper()
	return buildAddrHandler(t, allowed, nil, channels...)
}

// buildAddrHandler is newAddrHandler with a queue, which turns on the
// asynchronous path.
func buildAddrHandler(t *testing.T, allowed []string, q Enqueuer, channels ...config.ChannelConfig) http.Handler {
	t.Helper()
	registerFakeChannel()
	registerAddrChannel()
	resetTargets()

	rtr, err := router.New(router.Options{Channels: channels, DeliverTimeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("router.New: %v", err)
	}

	keys, err := (&config.Config{Auth: config.AuthConfig{
		APIKeys: []config.APIKeyConfig{{
			Name: "test", KeyHash: auth.HashAPIKey(testToken), AllowedRecipients: allowed,
		}},
	}}).Keys()
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}

	return NewHandler(Deps{
		Live:   NewLive(keys, 10*time.Second),
		Log:    slog.New(slog.DiscardHandler),
		Router: rtr,
		Queue:  q,
	})
}

// fakeQueue records what would be queued, so the accept path can be exercised
// without a store or a spool.
type fakeQueue struct {
	mu    sync.Mutex
	specs []queue.TargetSpec
}

func (q *fakeQueue) Enqueue(_ context.Context, requestID string, _ *message.Message, specs []queue.TargetSpec) ([]*store.Delivery, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.specs = append(q.specs, specs...)

	out := make([]*store.Delivery, 0, len(specs))
	for i, s := range specs {
		out = append(out, &store.Delivery{
			ID:          fmt.Sprintf("d%d", i),
			RequestID:   requestID,
			Target:      s.Target,
			Channel:     s.Channel,
			Recipients:  s.Recipients,
			ChannelType: s.ChannelType,
			Status:      store.StatusQueued,
		})
	}
	return out, nil
}

func (q *fakeQueue) queued() []queue.TargetSpec {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]queue.TargetSpec(nil), q.specs...)
}

func addrCfg(name string) config.ChannelConfig {
	return config.ChannelConfig{Name: name, Type: addrFakeType}
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v (body=%s)", err, rec.Body.String())
	}
	return body.Error
}

// A `to` field names recipients for a channel that takes them.
func TestNotify_ToFieldReachesTheChannel(t *testing.T) {
	h := newAddrHandler(t, []string{"@example.com"}, addrCfg("tx"))

	rec := post(t, h, testToken, map[string]any{
		"targets": []string{"tx"},
		"to":      []string{"user@example.com"},
		"title":   "Reset your password",
		"body":    "https://example.com/reset/abc",
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	got := targetsFor("tx")
	if len(got) != 1 {
		t.Fatalf("channel was called %d times, want 1", len(got))
	}
	if len(got[0].Recipients) != 1 || got[0].Recipients[0] != "user@example.com" {
		t.Errorf("recipients = %v, want [user@example.com]", got[0].Recipients)
	}
	if got[0].Instance != "tx" {
		t.Errorf("instance = %q, want tx", got[0].Instance)
	}
}

// The URL form is the other spelling of the same thing and must arrive
// identically.
func TestNotify_URLTargetIsEquivalentToTheToField(t *testing.T) {
	h := newAddrHandler(t, []string{"@example.com"}, addrCfg("tx"))

	rec := post(t, h, testToken, map[string]any{
		"targets": []string{addrFakeType + "://user@example.com?via=tx"},
		"title":   "Reset your password",
		"body":    "https://example.com/reset/abc",
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	got := targetsFor("tx")
	if len(got) != 1 {
		t.Fatalf("channel was called %d times, want 1", len(got))
	}
	if len(got[0].Recipients) != 1 || got[0].Recipients[0] != "user@example.com" {
		t.Errorf("recipients = %v, want [user@example.com]", got[0].Recipients)
	}
}

// The allow list is the guard that keeps the relay from being a way to mail
// anyone. Both spellings must go through it — checking only the `to` field
// would leave the URL form as a way round.
func TestNotify_AllowList(t *testing.T) {
	tests := []struct {
		name     string
		allowed  []string
		body     map[string]any
		wantCode int
		wantErr  string
	}{
		{
			name:     "a key with no list may not name recipients",
			allowed:  nil,
			body:     map[string]any{"targets": []string{"tx"}, "to": []string{"user@example.com"}},
			wantCode: http.StatusForbidden,
			wantErr:  "recipient_not_allowed",
		},
		{
			name:     "a key with no list may not use a URL either",
			allowed:  nil,
			body:     map[string]any{"targets": []string{addrFakeType + "://user@example.com?via=tx"}},
			wantCode: http.StatusForbidden,
			wantErr:  "recipient_not_allowed",
		},
		{
			name:     "a wildcard allows anything",
			allowed:  []string{"*"},
			body:     map[string]any{"targets": []string{"tx"}, "to": []string{"someone@anywhere.test"}},
			wantCode: http.StatusOK,
		},
		{
			name:     "a domain pattern allows that domain",
			allowed:  []string{"@example.com"},
			body:     map[string]any{"targets": []string{"tx"}, "to": []string{"user@example.com"}},
			wantCode: http.StatusOK,
		},
		{
			name:     "a domain pattern does not allow a lookalike",
			allowed:  []string{"@example.com"},
			body:     map[string]any{"targets": []string{"tx"}, "to": []string{"user@notexample.com"}},
			wantCode: http.StatusForbidden,
			wantErr:  "recipient_not_allowed",
		},
		{
			name:     "one bad address among several refuses the request",
			allowed:  []string{"@example.com"},
			body:     map[string]any{"targets": []string{"tx"}, "to": []string{"a@example.com", "b@elsewhere.test"}},
			wantCode: http.StatusForbidden,
			wantErr:  "recipient_not_allowed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newAddrHandler(t, tt.allowed, addrCfg("tx"))

			full := map[string]any{"title": "hi", "body": "there"}
			for k, v := range tt.body {
				full[k] = v
			}

			rec := post(t, h, testToken, full)
			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d (body=%s)", rec.Code, tt.wantCode, rec.Body.String())
			}
			if tt.wantErr != "" {
				if got := decodeError(t, rec); got != tt.wantErr {
					t.Errorf("error = %q, want %q", got, tt.wantErr)
				}
			}
			if tt.wantCode == http.StatusForbidden {
				if calls := len(targetsFor("tx")); calls != 0 {
					t.Errorf("the channel was called %d times on a refused request", calls)
				}
			}
		})
	}
}

// A target that cannot carry recipients must not silently drop them: the caller
// would believe a password-reset link reached a person.
//
// On the synchronous path this is reported as that target's outcome rather than
// as a status code, which is the contract the path has always had — and here it
// is also the more useful answer: a request that addresses an email channel and
// a chat channel still delivers the mail, instead of failing whole because one
// of its targets cannot take an address.
func TestNotify_RecipientsOnAChannelThatTakesNone(t *testing.T) {
	h := newAddrHandler(t, []string{"*"}, addrCfg("tx"), channelCfg("chat", nil))

	rec := post(t, h, testToken, map[string]any{
		"targets": []string{"chat", "tx"},
		"to":      []string{"user@example.com"},
		"title":   "hi",
		"body":    "there",
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}

	var resp notifyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("got %d results, want 2", len(resp.Results))
	}
	if got := resp.Results[0].Status; got != "permanent" {
		t.Errorf("the chat target = %q, want permanent", got)
	}
	if !strings.Contains(resp.Results[0].Error, "does not accept recipients") {
		t.Errorf("the refusal should say why, got %q", resp.Results[0].Error)
	}
	if got := resp.Results[1].Status; got != "sent" {
		t.Errorf("the addressable target = %q, want sent", got)
	}
}

// The asynchronous path cannot deliver half a request, so the same input is
// refused outright — before anything is queued, which is the point of
// validating at accept time.
func TestNotify_RecipientsNotSupportedIsRejectedAtAcceptTime(t *testing.T) {
	q := &fakeQueue{}
	h := buildAddrHandler(t, []string{"*"}, q, channelCfg("chat", nil))

	rec := post(t, h, testToken, map[string]any{
		"targets": []string{"chat"},
		"to":      []string{"user@example.com"},
		"title":   "hi",
		"body":    "there",
	})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
	if got := decodeError(t, rec); got != "recipients_not_supported" {
		t.Errorf("error = %q, want recipients_not_supported", got)
	}
	if got := len(q.queued()); got != 0 {
		t.Errorf("%d deliveries were queued for a refused request", got)
	}
}

// The queued record keeps the caller's own string for the reply, and the
// instance it resolved to for everything operational: quota, the breaker, the
// metrics label, and "show me everything for tx" in the operator UI.
func TestNotify_AsyncPinsTheResolvedInstance(t *testing.T) {
	q := &fakeQueue{}
	h := buildAddrHandler(t, []string{"@example.com"}, q, addrCfg("tx"))

	rec := post(t, h, testToken, map[string]any{
		"targets": []string{"tx"},
		"to":      []string{"user@example.com"},
		"title":   "hi",
		"body":    "there",
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body=%s)", rec.Code, rec.Body.String())
	}

	specs := q.queued()
	if len(specs) != 1 {
		t.Fatalf("queued %d specs, want 1", len(specs))
	}
	if specs[0].Target != "tx" {
		t.Errorf("Target = %q, want the caller's own string", specs[0].Target)
	}
	if specs[0].Channel != "tx" {
		t.Errorf("Channel = %q, want the resolved instance", specs[0].Channel)
	}
	if len(specs[0].Recipients) != 1 || specs[0].Recipients[0] != "user@example.com" {
		t.Errorf("Recipients = %v, want [user@example.com]", specs[0].Recipients)
	}
}

// Two answers to one question. Merging them would send to addresses the caller
// never wrote; picking one would silently ignore the other.
func TestNotify_ToFieldAndURLTargetConflict(t *testing.T) {
	h := newAddrHandler(t, []string{"*"}, addrCfg("tx"))

	rec := post(t, h, testToken, map[string]any{
		"targets": []string{addrFakeType + "://a@example.com?via=tx"},
		"to":      []string{"b@example.com"},
		"title":   "hi",
		"body":    "there",
	})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
	if got := decodeError(t, rec); got != "invalid_request" {
		t.Errorf("error = %q, want invalid_request", got)
	}
}

// An unknown target keeps the contract the synchronous path has always had: one
// result per target, and the ones that resolved are still delivered.
func TestNotify_UnknownTargetDoesNotHideTheOthers(t *testing.T) {
	h := newAddrHandler(t, []string{"*"}, addrCfg("tx"))

	rec := post(t, h, testToken, map[string]any{
		"targets": []string{"nope", "tx"},
		"title":   "hi",
		"body":    "there",
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}

	var resp notifyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("got %d results, want 2", len(resp.Results))
	}
	if resp.Results[0].Status != "permanent" {
		t.Errorf("the unknown target should be permanent, got %q", resp.Results[0].Status)
	}
	if resp.Results[0].Target != "nope" {
		t.Errorf("target echoed as %q, want the caller's own string", resp.Results[0].Target)
	}
	if resp.Results[1].Status != "sent" {
		t.Errorf("the resolvable target should still be delivered, got %q", resp.Results[1].Status)
	}
}
