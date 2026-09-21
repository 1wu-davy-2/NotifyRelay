package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"notifyrelay/internal/auth"
	"notifyrelay/internal/channel"
	"notifyrelay/internal/config"
	"notifyrelay/internal/message"
	"notifyrelay/internal/router"
)

// ------------------------------------------------------------------ fake channel

var registerFakeOnce sync.Once

// fakeChannel lets the API tests control delivery outcomes without involving
// a real transport. It is registered under its own type name so it cannot
// collide with a built-in channel.
type fakeChannel struct {
	fail  channel.ResultClass
	delay time.Duration
}

func fakeParamSchema() []channel.ParamSpec {
	return []channel.ParamSpec{
		{Name: "fail", Type: channel.ParamEnum, Values: []string{"sent", "permanent", "transient", "connect"}},
		{Name: "delay", Type: channel.ParamDuration},
	}
}

func registerFakeChannel() {
	registerFakeOnce.Do(func() {
		channel.Register(channel.Descriptor{
			Type:        "apitest",
			ParamSchema: fakeParamSchema(),
			Capability: channel.Capability{
				SupportedFormats: []message.Format{message.FormatText, message.FormatMarkdown, message.FormatHTML},
			},
			Factory: func(_ string, cfg map[string]any) (channel.Channel, error) {
				c := &fakeChannel{}
				switch cfg["fail"] {
				case "permanent":
					c.fail = channel.ClassPermanent
				case "transient":
					c.fail = channel.ClassTransient
				case "connect":
					c.fail = channel.ClassConnectError
				}
				if d, ok := cfg["delay"].(string); ok {
					if parsed, err := time.ParseDuration(d); err == nil {
						c.delay = parsed
					}
				}
				return c, nil
			},
		})
	})
}

func (c *fakeChannel) Type() string                     { return "apitest" }
func (c *fakeChannel) ParamSchema() []channel.ParamSpec { return fakeParamSchema() }

func (c *fakeChannel) Capability() channel.Capability {
	return channel.Capability{
		SupportedFormats: []message.Format{message.FormatText, message.FormatMarkdown, message.FormatHTML},
	}
}

func (c *fakeChannel) Send(ctx context.Context, _ *message.Message) channel.Result {
	if c.delay > 0 {
		select {
		case <-time.After(c.delay):
		case <-ctx.Done():
			return channel.ConnectError(ctx.Err(), "context expired")
		}
	}
	// The zero value means "not configured to fail". It used to be ClassSent;
	// ClassNotAttempted took that slot, and a double that fails by default
	// would be a trap.
	if c.fail != 0 && c.fail != channel.ClassSent {
		return channel.Result{Class: c.fail, Err: errors.New("fake failure"), Detail: "fake"}
	}
	return channel.Sent("fake ok")
}

func (c *fakeChannel) Test(context.Context) channel.Result { return channel.Sent("fake ok") }

// ----------------------------------------------------------------------- setup

const testToken = "test-token"

func newTestHandler(t *testing.T, channels ...config.ChannelConfig) (http.Handler, *router.Router) {
	t.Helper()
	return newTestHandlerWithTimeouts(t, 10*time.Second, 5*time.Second, channels...)
}

func newTestHandlerWithTimeouts(
	t *testing.T,
	handlerTimeout, deliverTimeout time.Duration,
	channels ...config.ChannelConfig,
) (http.Handler, *router.Router) {
	t.Helper()
	registerFakeChannel()

	rtr, err := router.New(router.Options{Channels: channels, DeliverTimeout: deliverTimeout})
	if err != nil {
		t.Fatalf("router.New: %v", err)
	}

	keys, err := (&config.Config{Auth: config.AuthConfig{
		APIKeys: []config.APIKeyConfig{{Name: "test", KeyHash: auth.HashAPIKey(testToken)}},
	}}).Keys()
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}

	return NewHandler(Deps{
		Keys:           keys,
		Log:            slog.New(slog.DiscardHandler),
		Router:         rtr,
		HandlerTimeout: handlerTimeout,
	}), rtr
}

func channelCfg(name string, cfg map[string]any) config.ChannelConfig {
	return config.ChannelConfig{Name: name, Type: "apitest", Config: cfg}
}

func post(t *testing.T, h http.Handler, token string, body any) *httptest.ResponseRecorder {
	t.Helper()

	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/notify", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// ------------------------------------------------------------------- the tests

func TestNotify_DeliversToEveryTarget(t *testing.T) {
	h, _ := newTestHandler(t,
		channelCfg("good", nil),
		channelCfg("also-good", nil),
	)

	rec := post(t, h, testToken, map[string]any{
		"targets": []string{"good", "also-good"},
		"title":   "deploy finished",
		"body":    "service x is live",
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp notifyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("got %d results, want 2", len(resp.Results))
	}
	for _, r := range resp.Results {
		if r.Status != "sent" {
			t.Errorf("target %s status = %q, want sent", r.Target, r.Status)
		}
	}
	if resp.RequestID == "" {
		t.Error("response has no request_id")
	}
}

// The whole point of a fan-out: one target failing must not hide the others,
// and the HTTP status must not collapse them into a single verdict.
func TestNotify_PartialFailureIsReportedPerTarget(t *testing.T) {
	h, _ := newTestHandler(t,
		channelCfg("good", nil),
		channelCfg("bad", map[string]any{"fail": "permanent"}),
	)

	rec := post(t, h, testToken, map[string]any{
		"targets": []string{"good", "bad"},
		"title":   "t",
		"body":    "b",
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the request was understood; per-target results carry the outcome)", rec.Code)
	}

	var resp notifyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	status := map[string]string{}
	for _, r := range resp.Results {
		status[r.Target] = r.Status
	}
	if status["good"] != "sent" {
		t.Errorf("good target status = %q, want sent", status["good"])
	}
	if status["bad"] != "permanent" {
		t.Errorf("bad target status = %q, want permanent", status["bad"])
	}
}

func TestNotify_UnknownTargetIsReportedNotFatal(t *testing.T) {
	h, _ := newTestHandler(t, channelCfg("good", nil))

	rec := post(t, h, testToken, map[string]any{
		"targets": []string{"good", "nope"},
		"title":   "t",
		"body":    "b",
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var resp notifyResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)

	for _, r := range resp.Results {
		if r.Target == "nope" && r.Status != "permanent" {
			t.Errorf("unknown target status = %q, want permanent", r.Status)
		}
	}
}

func TestNotify_RequiresAValidKey(t *testing.T) {
	h, _ := newTestHandler(t, channelCfg("good", nil))

	body := map[string]any{"targets": []string{"good"}, "title": "t", "body": "b"}

	tests := []struct {
		name  string
		token string
		want  int
	}{
		{name: "no key", token: "", want: http.StatusUnauthorized},
		{name: "wrong key", token: "not-the-token", want: http.StatusUnauthorized},
		{name: "right key", token: testToken, want: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if rec := post(t, h, tt.token, body); rec.Code != tt.want {
				t.Errorf("status = %d, want %d", rec.Code, tt.want)
			}
		})
	}
}

func TestNotify_RejectsMalformedRequests(t *testing.T) {
	h, _ := newTestHandler(t, channelCfg("good", nil))

	tests := []struct {
		name string
		body any
	}{
		{name: "no targets", body: map[string]any{"title": "t", "body": "b"}},
		{name: "no title", body: map[string]any{"targets": []string{"good"}, "body": "b"}},
		{name: "no body", body: map[string]any{"targets": []string{"good"}, "title": "t"}},
		{name: "bad format", body: map[string]any{"targets": []string{"good"}, "title": "t", "body": "b", "format": "rtf"}},
		{name: "bad type", body: map[string]any{"targets": []string{"good"}, "title": "t", "body": "b", "type": "urgent"}},
		{name: "priority out of range", body: map[string]any{"targets": []string{"good"}, "title": "t", "body": "b", "priority": 9}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if rec := post(t, h, testToken, tt.body); rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestNotify_RejectsUnknownFields(t *testing.T) {
	h, _ := newTestHandler(t, channelCfg("good", nil))

	raw := `{"targets":["good"],"title":"t","body":"b","typo_field":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/notify", bytes.NewReader([]byte(raw)))
	req.Header.Set("Authorization", "Bearer "+testToken)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400: a misspelled field must not be silently ignored", rec.Code)
	}
}

// The handler timeout must cut off a slow target rather than hanging forever.
func TestNotify_HandlerTimeoutIsEnforced(t *testing.T) {
	// The handler deadline is deliberately shorter than the per-target one, to
	// prove the outer layer is the one doing the cutting.
	h, _ := newTestHandlerWithTimeouts(t, 300*time.Millisecond, 30*time.Second,
		channelCfg("slow", map[string]any{"delay": "10s"}),
	)

	start := time.Now()
	rec := post(t, h, testToken, map[string]any{
		"targets": []string{"slow"},
		"title":   "t",
		"body":    "b",
	})

	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("request took %v; the handler timeout did not apply", elapsed)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	var resp notifyResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Results) != 1 || resp.Results[0].Status == "sent" {
		t.Errorf("a timed-out target must not be reported as sent: %+v", resp.Results)
	}
}

func TestHealthEndpointsNeedNoKey(t *testing.T) {
	h, _ := newTestHandler(t)

	for _, path := range []string{"/healthz", "/readyz"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("%s status = %d, want 200 without a key", path, rec.Code)
		}
		if body, _ := io.ReadAll(rec.Body); len(body) == 0 {
			t.Errorf("%s returned an empty body", path)
		}
	}
}
