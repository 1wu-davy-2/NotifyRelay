package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"notifyrelay/internal/channel"
	"notifyrelay/internal/message"
)

// capture is a receiver that records what it was sent and replies with a
// scripted status.
type capture struct {
	mu       sync.Mutex
	requests []recorded
	status   int
	body     string
	headers  http.Header
}

type recorded struct {
	Method      string
	Path        string
	ContentType string
	Headers     http.Header
	Body        []byte
}

func newCapture() *capture { return &capture{status: http.StatusOK} }

func (c *capture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)

	c.mu.Lock()
	c.requests = append(c.requests, recorded{
		Method:      r.Method,
		Path:        r.URL.Path,
		ContentType: r.Header.Get("Content-Type"),
		Headers:     r.Header.Clone(),
		Body:        body,
	})
	status, reply := c.status, c.body
	c.mu.Unlock()

	w.WriteHeader(status)
	_, _ = w.Write([]byte(reply))
}

func (c *capture) last(t *testing.T) recorded {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.requests) == 0 {
		t.Fatal("the endpoint received nothing")
	}
	return c.requests[len(c.requests)-1]
}

func (c *capture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.requests)
}

func newChannel(t *testing.T, cfg map[string]any) *Channel {
	t.Helper()
	ch, err := New("test", cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return ch.(*Channel)
}

func sampleMessage() *message.Message {
	m := &message.Message{
		Title:    "数据库主从延迟告警",
		Body:     "延迟 12s",
		Format:   message.FormatMarkdown,
		Type:     message.TypeWarning,
		Tags:     []string{"db"},
		Priority: 4,
	}
	m.Normalize()
	return m
}

func TestSend_PostsTheDefaultPayload(t *testing.T) {
	rec := newCapture()
	srv := httptest.NewServer(rec)
	defer srv.Close()

	ch := newChannel(t, map[string]any{"url": srv.URL})
	res := ch.Send(context.Background(), sampleMessage())

	if res.Class != channel.ClassSent {
		t.Fatalf("class = %v (%v)", res.Class, res.Err)
	}

	got := rec.last(t)
	if got.Method != http.MethodPost {
		t.Errorf("method = %s, want POST", got.Method)
	}
	if !strings.HasPrefix(got.ContentType, "application/json") {
		t.Errorf("content type = %q", got.ContentType)
	}

	var payload map[string]any
	if err := json.Unmarshal(got.Body, &payload); err != nil {
		t.Fatalf("payload is not JSON: %v\n%s", err, got.Body)
	}
	if payload["title"] != "数据库主从延迟告警" {
		t.Errorf("title = %v", payload["title"])
	}
	if payload["type"] != "warning" {
		t.Errorf("type = %v", payload["type"])
	}
	if payload["format"] != "markdown" {
		t.Errorf("format = %v", payload["format"])
	}
	if payload["source"] != "test" {
		t.Errorf("source = %v, want the instance name", payload["source"])
	}
}

func TestSend_PayloadTemplate(t *testing.T) {
	rec := newCapture()
	srv := httptest.NewServer(rec)
	defer srv.Close()

	ch := newChannel(t, map[string]any{
		"url":              srv.URL,
		"payload_template": `{"text": "{title}: {body}", "level": "x"}`,
	})

	if res := ch.Send(context.Background(), sampleMessage()); res.Class != channel.ClassSent {
		t.Fatalf("class = %v (%v)", res.Class, res.Err)
	}

	var payload map[string]string
	if err := json.Unmarshal(rec.last(t).Body, &payload); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	if payload["text"] != "数据库主从延迟告警: 延迟 12s" {
		t.Errorf("text = %q", payload["text"])
	}
}

// A body containing a quote or a newline must not be able to break the JSON
// envelope. The failure would otherwise surface at the receiving end, far from
// the template that caused it.
func TestSend_PayloadTemplateEscapesValues(t *testing.T) {
	rec := newCapture()
	srv := httptest.NewServer(rec)
	defer srv.Close()

	ch := newChannel(t, map[string]any{
		"url":              srv.URL,
		"payload_template": `{"text": "{body}"}`,
	})

	msg := sampleMessage()
	msg.Body = "line one\nline \"two\" \\ three"

	if res := ch.Send(context.Background(), msg); res.Class != channel.ClassSent {
		t.Fatalf("class = %v (%v)", res.Class, res.Err)
	}

	var payload map[string]string
	if err := json.Unmarshal(rec.last(t).Body, &payload); err != nil {
		t.Fatalf("the escaped payload is not JSON: %v\n%s", err, rec.last(t).Body)
	}
	if payload["text"] != msg.Body {
		t.Errorf("text = %q, want %q", payload["text"], msg.Body)
	}
}

func TestSend_RejectsATemplateThatIsNotJSON(t *testing.T) {
	rec := newCapture()
	srv := httptest.NewServer(rec)
	defer srv.Close()

	ch := newChannel(t, map[string]any{
		"url":              srv.URL,
		"payload_template": `not json at all`,
	})

	res := ch.Send(context.Background(), sampleMessage())
	if res.Class != channel.ClassPermanent {
		t.Fatalf("class = %v, want PERMANENT", res.Class)
	}
	if rec.count() != 0 {
		t.Error("a broken template must not reach the endpoint")
	}
}

func TestSend_Authentication(t *testing.T) {
	const secret = "s3cret"

	tests := []struct {
		name   string
		cfg    map[string]any
		verify func(*testing.T, recorded)
	}{
		{
			name: "bearer",
			cfg:  map[string]any{"auth_type": "bearer", "token": "tok"},
			verify: func(t *testing.T, r recorded) {
				if got := r.Headers.Get("Authorization"); got != "Bearer tok" {
					t.Errorf("Authorization = %q", got)
				}
			},
		},
		{
			name: "basic",
			cfg:  map[string]any{"auth_type": "basic", "username": "u", "password": "p"},
			verify: func(t *testing.T, r recorded) {
				header := r.Headers.Get("Authorization")
				if !strings.HasPrefix(header, "Basic ") {
					t.Fatalf("Authorization = %q, want a Basic header", header)
				}
				decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(header, "Basic "))
				if err != nil {
					t.Fatalf("decode credentials: %v", err)
				}
				if string(decoded) != "u:p" {
					t.Errorf("credentials = %q, want u:p", decoded)
				}
			},
		},
		{
			name: "custom header",
			cfg:  map[string]any{"auth_type": "header", "header_name": "X-Source", "header_value": "relay"},
			verify: func(t *testing.T, r recorded) {
				if got := r.Headers.Get("X-Source"); got != "relay" {
					t.Errorf("X-Source = %q", got)
				}
			},
		},
		{
			name: "hmac signature",
			cfg: map[string]any{
				"auth_type": "hmac", "secret": secret,
				"signature_header": "X-Signature", "signature_prefix": "sha256=",
			},
			verify: func(t *testing.T, r recorded) {
				sig := hmac.New(sha256.New, []byte(secret))
				sig.Write(r.Body)
				want := "sha256=" + hex.EncodeToString(sig.Sum(nil))
				if got := r.Headers.Get("X-Signature"); got != want {
					t.Errorf("X-Signature = %q, want %q", got, want)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := newCapture()
			srv := httptest.NewServer(rec)
			defer srv.Close()

			cfg := map[string]any{"url": srv.URL}
			for k, v := range tt.cfg {
				cfg[k] = v
			}

			ch := newChannel(t, cfg)
			if res := ch.Send(context.Background(), sampleMessage()); res.Class != channel.ClassSent {
				t.Fatalf("class = %v (%v)", res.Class, res.Err)
			}
			tt.verify(t, rec.last(t))
		})
	}
}

// ------------------------------------------------------------ classification

func TestSend_ClassifiesResponses(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   channel.ResultClass
	}{
		{name: "200", status: http.StatusOK, want: channel.ClassSent},
		{name: "201", status: http.StatusCreated, want: channel.ClassSent},
		{name: "204", status: http.StatusNoContent, want: channel.ClassSent},

		// The endpoint is asking us to come back.
		{name: "408", status: http.StatusRequestTimeout, want: channel.ClassTransient},
		{name: "429", status: http.StatusTooManyRequests, want: channel.ClassTransient},
		{name: "500", status: http.StatusInternalServerError, want: channel.ClassTransient},
		{name: "503", status: http.StatusServiceUnavailable, want: channel.ClassTransient},

		// The request itself is wrong; repeating it changes nothing.
		{name: "400", status: http.StatusBadRequest, want: channel.ClassPermanent},
		{name: "401", status: http.StatusUnauthorized, want: channel.ClassPermanent},
		{name: "404", status: http.StatusNotFound, want: channel.ClassPermanent},
		{name: "422", status: http.StatusUnprocessableEntity, want: channel.ClassPermanent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := newCapture()
			rec.status = tt.status
			rec.body = "detail from the endpoint"
			srv := httptest.NewServer(rec)
			defer srv.Close()

			ch := newChannel(t, map[string]any{"url": srv.URL})
			res := ch.Send(context.Background(), sampleMessage())

			if res.Class != tt.want {
				t.Errorf("class = %v, want %v (detail: %s)", res.Class, tt.want, res.Detail)
			}
			if tt.want != channel.ClassSent && !strings.Contains(res.Detail, strconv.Itoa(tt.status)) {
				t.Errorf("detail should carry the status, got %q", res.Detail)
			}
		})
	}
}

func TestSend_RetryAfterIsSurfacedInTheDetail(t *testing.T) {
	rec := newCapture()
	rec.status = http.StatusTooManyRequests
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		rec.ServeHTTP(w, r)
	}))
	defer srv.Close()

	ch := newChannel(t, map[string]any{"url": srv.URL})
	res := ch.Send(context.Background(), sampleMessage())

	if res.Class != channel.ClassTransient {
		t.Fatalf("class = %v, want TRANSIENT", res.Class)
	}
	if !strings.Contains(res.Detail, "30") {
		t.Errorf("detail should carry the retry hint, got %q", res.Detail)
	}
}

func TestSend_UnreachableEndpointIsConnectError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // nothing is listening now

	ch := newChannel(t, map[string]any{"url": url, "timeout": "2s"})
	res := ch.Send(context.Background(), sampleMessage())

	if res.Class != channel.ClassConnectError {
		t.Fatalf("class = %v (%s), want CONNECT_ERROR", res.Class, res.Detail)
	}
}

// -------------------------------------------------------------------- config

func TestNew_RejectsBadConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		cfg     map[string]any
		wantSub string
	}{
		{name: "missing url", cfg: map[string]any{}, wantSub: "url"},
		{name: "non-http scheme", cfg: map[string]any{"url": "ftp://example.com"}, wantSub: "http"},
		{name: "no host", cfg: map[string]any{"url": "http://"}, wantSub: "host"},
		{name: "bad method", cfg: map[string]any{"url": "http://x.y", "method": "DELETE"}, wantSub: "method"},
		{name: "bad overflow mode", cfg: map[string]any{"url": "http://x.y", "overflow_mode": "shrink"}, wantSub: "overflow_mode"},
		{name: "negative body limit", cfg: map[string]any{"url": "http://x.y", "body_max_len": -1}, wantSub: "at least 0"},
		{name: "bearer without token", cfg: map[string]any{"url": "http://x.y", "auth_type": "bearer"}, wantSub: "token"},
		{name: "hmac without secret", cfg: map[string]any{"url": "http://x.y", "auth_type": "hmac"}, wantSub: "secret"},
		{name: "unknown auth", cfg: map[string]any{"url": "http://x.y", "auth_type": "voodoo"}, wantSub: "auth_type"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New("test", tt.cfg)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("error %q should mention %q", err, tt.wantSub)
			}
		})
	}
}

func TestCapability_ReflectsConfiguredLimits(t *testing.T) {
	ch := newChannel(t, map[string]any{
		"url":           "http://example.com",
		"body_max_len":  100,
		"title_max_len": 20,
		"overflow_mode": "split",
		"rate_per_sec":  2.5,
	})

	cap := ch.Capability()
	if cap.BodyMaxLen != 100 || cap.TitleMaxLen != 20 {
		t.Errorf("limits = %d/%d, want 100/20", cap.BodyMaxLen, cap.TitleMaxLen)
	}
	if cap.OverflowMode != channel.OverflowSplit {
		t.Errorf("overflow mode = %v", cap.OverflowMode)
	}
	if cap.RatePerSec != 2.5 {
		t.Errorf("rate = %v, want 2.5", cap.RatePerSec)
	}
}
